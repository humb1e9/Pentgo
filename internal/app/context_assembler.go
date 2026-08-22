package app

import (
	"context"
	"errors"
	"fmt"

	"pentgo/internal/agent"
	"pentgo/internal/config"
)

// ErrContextPreflight rejects a provider request whose fixed or compacted
// context still exceeds its configured model-context threshold.
var ErrContextPreflight = errors.New("context request exceeds configured budget")

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
func (assembler *ContextAssembler) Prepare(ctx context.Context, sessionID, systemPrompt string, tools []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error) {
	if assembler == nil || assembler.runtime == nil || assembler.meter == nil {
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
	surfaceStore := assembler.runtime.ContextSurface(sessionID)
	if surfaceStore == nil {
		return agent.ModelStepInput{}, nil, fmt.Errorf("session context surface is unavailable")
	}
	messages := numberedMessages(transcript.Messages())
	facts := RenderBoundedBlackboard(assembler.runtime.Blackboard(), int(float64(assembler.policy.ContextWindow)*assembler.policy.BlackboardRatio))
	activities := make([]agent.ContextActivity, 0, 3)
	if facts.Truncated {
		activities = append(activities, agent.ContextActivity{Kind: agent.ContextBlackboardLimited, Message: fmt.Sprintf("已限制项目事实注入（显示 %d，省略 %d）。", facts.Shown, facts.Omitted)})
	}
	surface, err := surfaceStore.Snapshot()
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	input, err := assembler.materialize(sessionID, systemPrompt, tools, facts.Text, surface, messages)
	if err != nil {
		return agent.ModelStepInput{}, activities, err
	}
	measurement := assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens < contextThreshold(assembler.policy) {
		return input, activities, nil
	}
	fixed := assembler.meter.Measure(ContextRequest{SystemPrompt: systemPrompt, Blackboard: facts.Text, Tools: tools})
	if fixed.TotalTokens >= contextThreshold(assembler.policy) {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity("系统提示词、工具或项目事实本身超过上下文预算。")), ErrContextPreflight
	}
	compactor := NewContextCompactor(assembler.policy, surfaceStore, assembler.meter, assembler.summarizer)
	pruned, pruneActivities, err := compactor.Prune(ctx, CompactionRequest{Surface: surface, Messages: messages, SystemPrompt: systemPrompt, Blackboard: facts.Text, Tools: tools})
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
		return input, activities, nil
	}
	if assembler.summarizer == nil {
		return agent.ModelStepInput{}, append(activities, rejectedContextActivity("无法在没有 checkpoint summarizer 的情况下压缩上下文。")), ErrContextPreflight
	}
	checkpointed, checkpointActivities, err := compactor.Checkpoint(ctx, CompactionRequest{Surface: pruned, Messages: messages, SystemPrompt: systemPrompt, Blackboard: facts.Text, Tools: tools})
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
		SessionID:     sessionID,
		SystemPrompt:  systemPrompt,
		ProjectFacts:  facts,
		Tools:         append([]agent.Tool(nil), tools...),
		ContextWindow: assembler.policy.ContextWindow,
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
	nodes := make([]agent.SurfaceNode, 0, len(input.Messages))
	messages := make(map[int]agent.Message, len(input.Messages))
	for index, message := range input.Messages {
		sequence := index + 1
		nodes = append(nodes, agent.SurfaceNode{Kind: agent.SurfaceNodeSource, SourceStartSeq: sequence, SourceEndSeq: sequence})
		messages[sequence] = message
	}
	return ContextRequest{SystemPrompt: input.SystemPrompt, Blackboard: input.ProjectFacts, Tools: input.Tools, Nodes: nodes, Messages: messages}
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
