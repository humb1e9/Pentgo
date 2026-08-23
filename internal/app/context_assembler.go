package app

import (
	"context"
	"errors"
	"fmt"

	"pentgo/internal/adapters/llm"
	"pentgo/internal/agent"
	"pentgo/internal/config"
)

// ErrContextPreflight rejects a provider request whose fixed or compacted
// context still exceeds its configured model-context threshold.
var ErrContextPreflight = errors.New("context request exceeds configured budget")

// ContextPreparer is the host-loop seam for normal and overflow-recovery
// requests. Implementations must return a meaningfully compacted retry input.
type ContextPreparer interface {
	Prepare(context.Context, string, string, []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error)
	PrepareOverflowRecovery(context.Context, string, string, []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error)
}

// ContextAssembler materializes the persistent Context Surface immediately
// before each provider request. It never modifies the raw transcript ledger.
type ContextAssembler struct {
	runtime    *ProjectRuntime
	policy     config.AgentContextConfig
	meter      ContextMeter
	summarizer CheckpointSummarizer
}

// NewContextAssembler constructs the request-preflight coordinator. A zero
// ContextWindow keeps the legacy full-transcript replay path.
func NewContextAssembler(runtime *ProjectRuntime, policy config.AgentContextConfig, meter ContextMeter, summarizer CheckpointSummarizer) *ContextAssembler {
	return &ContextAssembler{runtime: runtime, policy: policy.Effective(), meter: meter, summarizer: summarizer}
}

// Prepare returns one model-visible request and any non-transcript context
// activities. The enabled path always measures a freshly materialized Surface.
func (assembler *ContextAssembler) PrepareOverflowRecovery(ctx context.Context, sessionID, systemPrompt string, tools []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error) {
	if assembler == nil || assembler.runtime == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("context assembler dependencies are incomplete")
	}
	if !assembler.policy.Enabled() {
		return agent.ModelStepInput{}, []agent.ContextActivity{{Kind: agent.ContextOverflowRetry, Message: "模型报告上下文超限，但当前上下文策略未启用压缩。"}}, ErrContextPreflight
	}
	if assembler.meter == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("context assembler meter is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	systemPrefix := llm.SystemInstructionPrefix(systemPrompt)
	surfaceStore := assembler.runtime.ContextSurface(sessionID)
	if surfaceStore == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("session context surface is unavailable")
	}
	surface, transcriptMessages, err := surfaceStore.SnapshotWithTranscript()
	if err != nil {
		return agent.ModelStepInput{}, nil, err
	}
	messages := numberedMessages(transcriptMessages)
	facts := RenderBoundedBlackboard(assembler.runtime.Blackboard(), int(float64(assembler.policy.ContextWindow)*assembler.policy.BlackboardRatio))
	factsEnvelope := llm.ProjectFactsEnvelope(facts.Text)
	compactor := NewContextCompactor(assembler.policy, surfaceStore, assembler.meter, assembler.summarizer)
	pruned, prunes, activities, err := compactor.PreviewPrune(ctx, CompactionRequest{Surface: surface, Messages: messages, SystemPrompt: systemPrefix, Blackboard: factsEnvelope, Tools: tools})
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	prunedInput, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, pruned, messages)
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	if measurement := assembler.meter.Measure(contextRequestFromInput(prunedInput)); measurement.TotalTokens < contextThreshold(assembler.policy) {
		if len(prunes) == 0 {
			return prunedInput, append(activities, agent.ContextActivity{Kind: agent.ContextOverflowRetry, Message: "Provider 报告上下文超限，但当前上下文已经在安全预算内。"}), nil
		}
		updated, err := surfaceStore.PruneTools(surface.Generation, prunes)
		if err != nil {
			return agent.ModelStepInput{}, activities, err
		}
		input, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, updated, messages)
		return input, append(activities, agent.ContextActivity{Kind: agent.ContextOverflowRetry, Message: "Provider 报告上下文超限，已裁剪工具结果后重试。"}), err
	}
	if assembler.summarizer == nil {
		return agent.ModelStepInput{}, append(activities, agent.ContextActivity{Kind: agent.ContextOverflowRetry, Message: "Provider 报告上下文超限，但没有可用 checkpoint summarizer。"}), ErrContextPreflight
	}
	checkpointed, checkpointActivities, err := compactor.CheckpointWithValidator(ctx, CompactionRequest{Surface: pruned, Messages: messages, SystemPrompt: systemPrefix, Blackboard: factsEnvelope, Tools: tools, Prunes: prunes}, func(candidate agent.ContextSurface) error {
		candidateInput, materializeErr := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, candidate, messages)
		if materializeErr != nil {
			return materializeErr
		}
		if measurement := assembler.meter.Measure(contextRequestFromInput(candidateInput)); measurement.TotalTokens >= contextThreshold(assembler.policy) {
			return ErrContextPreflight
		}
		return nil
	})
	activities = append(activities, checkpointActivities...)
	if err != nil {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	activities = append(activities, agent.ContextActivity{Kind: agent.ContextOverflowRetry, Message: "Provider 报告上下文超限，已创建 checkpoint 后重试。"})
	input, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, checkpointed, messages)
	return input, activities, err
}

func (assembler *ContextAssembler) Prepare(ctx context.Context, sessionID, systemPrompt string, tools []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error) {
	if assembler == nil || assembler.runtime == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("context assembler dependencies are incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	transcript := assembler.runtime.Transcript(sessionID)
	if transcript == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("session transcript is unavailable")
	}
	if !assembler.policy.Enabled() {
		return agent.ModelStepInput{
			SessionID:    sessionID,
			Messages:     cloneContextMessageSlice(transcript.Messages()),
			SystemPrompt: systemPrompt,
			ProjectFacts: blackboardText(assembler.runtime.Blackboard()),
			Tools:        append([]agent.Tool(nil), tools...),
		}, nil, nil
	}
	if assembler.meter == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("context assembler meter is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	systemPrefix := llm.SystemInstructionPrefix(systemPrompt)
	surfaceStore := assembler.runtime.ContextSurface(sessionID)
	if surfaceStore == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("session context surface is unavailable")
	}
	surface, transcriptMessages, err := surfaceStore.SnapshotWithTranscript()
	if err != nil {
		return agent.ModelStepInput{}, nil, err
	}
	messages := numberedMessages(transcriptMessages)
	facts := RenderBoundedBlackboard(assembler.runtime.Blackboard(), int(float64(assembler.policy.ContextWindow)*assembler.policy.BlackboardRatio))
	factsEnvelope := llm.ProjectFactsEnvelope(facts.Text)
	activities := make([]agent.ContextActivity, 0, 3)
	if facts.Truncated {
		activities = append(activities, agent.ContextActivity{Kind: agent.ContextBlackboardLimited, Message: fmt.Sprintf("已限制项目事实注入（显示 %d，省略 %d）。", facts.Shown, facts.Omitted)})
	}
	input, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, surface, messages)
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	measurement := assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens < contextThreshold(assembler.policy) {
		return input, activities, nil
	}
	fixed := assembler.meter.Measure(ContextRequest{SystemPrompt: systemPrefix, Blackboard: factsEnvelope, Tools: tools})
	if fixed.TotalTokens >= contextThreshold(assembler.policy) {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity("系统提示词、工具或项目事实本身超过上下文预算。")), ErrContextPreflight
	}
	compactor := NewContextCompactor(assembler.policy, surfaceStore, assembler.meter, assembler.summarizer)
	pruned, prunes, pruneActivities, err := compactor.PreviewPrune(ctx, CompactionRequest{Surface: surface, Messages: messages, SystemPrompt: systemPrefix, Blackboard: factsEnvelope, Tools: tools})
	activities = append(activities, pruneActivities...)
	if err != nil {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	input, err = assembler.materialize(sessionID, systemPrompt, tools, facts.Text, pruned, messages)
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	measurement = assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens < contextThreshold(assembler.policy) {
		if len(prunes) != 0 {
			if _, err := surfaceStore.PruneTools(surface.Generation, prunes); err != nil {
				return agent.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
			}
		}
		return input, activities, nil
	}
	if assembler.summarizer == nil {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity("无法在没有 checkpoint summarizer 的情况下压缩上下文。")), ErrContextPreflight
	}
	checkpointRequest := CompactionRequest{Surface: pruned, Messages: messages, SystemPrompt: systemPrefix, Blackboard: factsEnvelope, Tools: tools, Prunes: prunes}
	checkpointed, checkpointActivities, err := compactor.CheckpointWithValidator(ctx, checkpointRequest, func(candidate agent.ContextSurface) error {
		candidateInput, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, candidate, messages)
		if err != nil {
			return err
		}
		if measurement := assembler.meter.Measure(contextRequestFromInput(candidateInput)); measurement.TotalTokens >= contextThreshold(assembler.policy) {
			return ErrContextPreflight
		}
		return nil
	})
	activities = append(activities, checkpointActivities...)
	if err != nil {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	input, err = assembler.materialize(sessionID, systemPrompt, tools, facts.Text, checkpointed, messages)
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	measurement = assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens >= contextThreshold(assembler.policy) {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity("checkpoint 后上下文仍超过预算。")), ErrContextPreflight
	}
	return input, activities, nil
}

func (assembler *ContextAssembler) materialize(sessionID, systemPrompt string, tools []agent.Tool, facts string, surface agent.ContextSurface, messages map[int]agent.Message) (agent.ModelStepInput, error) {
	result := agent.ModelStepInput{
		SessionID:       sessionID,
		SystemPrompt:    systemPrompt,
		ProjectFacts:    facts,
		Tools:           append([]agent.Tool(nil), tools...),
		ContextWindow:   assembler.policy.ContextWindow,
		SurfaceNodes:    append([]agent.SurfaceNode(nil), surface.Nodes...),
		SurfaceMessages: cloneContextMessages(messages),
	}
	for _, node := range surface.Nodes {
		switch node.Kind {
		case agent.SurfaceNodeCheckpoint:
			result.Messages = append(result.Messages, agent.Message{Role: agent.RoleUser, Content: node.Content})
		case agent.SurfaceNodeSource, agent.SurfaceNodePrunedTool:
			if node.SourceStartSeq != node.SourceEndSeq {
				return agent.ModelStepInput{}, fmt.Errorf("surface node %q has a non-materializable source range", node.ID)
			}
			message, found := messages[node.SourceStartSeq]
			if !found {
				return agent.ModelStepInput{}, fmt.Errorf("surface source message #%d is unavailable", node.SourceStartSeq)
			}
			message = cloneContextMessages(map[int]agent.Message{node.SourceStartSeq: message})[node.SourceStartSeq]
			if node.Kind == agent.SurfaceNodePrunedTool {
				if message.Role != agent.RoleTool {
					return agent.ModelStepInput{}, fmt.Errorf("pruned surface node %q does not reference a tool result", node.ID)
				}
				message.Content = node.Content
			}
			result.Messages = append(result.Messages, message)
		default:
			return agent.ModelStepInput{}, fmt.Errorf("surface node %q has unknown kind %q", node.ID, node.Kind)
		}
	}
	return result, nil
}

func contextRequestFromInput(input agent.ModelStepInput) ContextRequest {
	nodes := append([]agent.SurfaceNode(nil), input.SurfaceNodes...)
	messages := cloneContextMessages(input.SurfaceMessages)
	if len(nodes) == 0 {
		nodes = make([]agent.SurfaceNode, 0, len(input.Messages))
		messages = make(map[int]agent.Message, len(input.Messages))
		for index, message := range input.Messages {
			sequence := index + 1
			nodes = append(nodes, agent.SurfaceNode{Kind: agent.SurfaceNodeSource, SourceStartSeq: sequence, SourceEndSeq: sequence})
			messages[sequence] = message
		}
	}
	return ContextRequest{SystemPrompt: llm.SystemInstructionPrefix(input.SystemPrompt), Blackboard: llm.ProjectFactsEnvelope(input.ProjectFacts), Tools: input.Tools, Nodes: nodes, Messages: messages}
}

func numberedMessages(messages []agent.Message) map[int]agent.Message {
	result := make(map[int]agent.Message, len(messages))
	for index, message := range messages {
		result[index+1] = message
	}
	return result
}

func cloneContextMessageSlice(messages []agent.Message) []agent.Message {
	numbered := numberedMessages(messages)
	cloned := cloneContextMessages(numbered)
	result := make([]agent.Message, len(messages))
	for index := range result {
		result[index] = cloned[index+1]
	}
	return result
}

func rejectedContextActivity(message string) agent.ContextActivity {
	return agent.ContextActivity{Kind: agent.ContextRequestRejected, Message: message}
}
