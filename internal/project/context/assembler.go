package context

import (
	"context"
	"errors"
	"fmt"

	"pentgo/internal/core"
	llm "pentgo/internal/model"
)

// ErrContextPreflight rejects a provider request whose fixed or compacted
// context still exceeds its configured model-context threshold.
var ErrContextPreflight = errors.New("context request exceeds configured budget")

// ContextPreparer is the host-loop seam for normal and overflow-recovery
// requests. Implementations must return a meaningfully compacted retry input.
type ContextPreparer interface {
	Prepare(context.Context, string, string, []core.Tool, string) (core.ModelStepInput, []core.ContextActivity, error)
	PrepareOverflowRecovery(context.Context, string, string, []core.Tool, string) (core.ModelStepInput, []core.ContextActivity, error)
}

// SessionSource supplies the persistent conversation and Context Surface for one session.
type SessionSource interface {
	Conversation(string) ([]core.Message, error)
	ContextSurface(string) (*ContextSurfaceStore, error)
}

// ContextAssembler materializes the persistent Context Surface immediately
// before each provider request. It never modifies the raw conversation ledger.
type ContextAssembler struct {
	source     SessionSource
	policy     Config
	meter      Meter
	summarizer CheckpointSummarizer
}

// NewContextAssembler constructs the request-preflight coordinator. A zero
// ContextWindow keeps the legacy full-conversation replay path.
func NewContextAssembler(source SessionSource, policy Config, meter Meter, summarizer CheckpointSummarizer) *ContextAssembler {
	return &ContextAssembler{source: source, policy: policy.Effective(), meter: meter, summarizer: summarizer}
}

// Prepare returns one model-visible request and any non-conversation context
// activities. The enabled path always measures a freshly materialized Surface.
func (assembler *ContextAssembler) PrepareOverflowRecovery(ctx context.Context, sessionID, systemPrompt string, tools []core.Tool, facts string) (core.ModelStepInput, []core.ContextActivity, error) {
	if assembler == nil || assembler.source == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("context assembler dependencies are incomplete")
	}
	if !assembler.policy.Enabled() {
		return core.ModelStepInput{}, []core.ContextActivity{{Kind: core.ContextOverflowRetry, Message: "模型报告上下文超限，但当前上下文策略未启用压缩。"}}, ErrContextPreflight
	}
	if assembler.meter == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("context assembler meter is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	systemPrefix := llm.SystemInstructionPrefix(systemPrompt)
	surfaceStore, err := assembler.source.ContextSurface(sessionID)
	if err != nil {
		return core.ModelStepInput{}, nil, err
	}
	if surfaceStore == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("session context surface is unavailable")
	}
	surface, conversationMessages, err := surfaceStore.SnapshotWithConversation()
	if err != nil {
		return core.ModelStepInput{}, nil, err
	}
	messages := numberedMessages(conversationMessages)
	factsEnvelope := llm.ProjectFactsEnvelope(facts)
	compactor := NewContextCompactor(assembler.policy, surfaceStore, assembler.meter, assembler.summarizer)
	pruned, prunes, activities, err := compactor.PreviewPrune(ctx, CompactionRequest{Surface: surface, Messages: messages, SystemPrompt: systemPrefix, ProjectFacts: factsEnvelope, Tools: tools})
	if err != nil {
		return core.ModelStepInput{}, activities, err
	}
	prunedInput, err := assembler.materialize(sessionID, systemPrompt, tools, facts, pruned, messages)
	if err != nil {
		return core.ModelStepInput{}, activities, err
	}
	if measurement := assembler.meter.Measure(contextRequestFromInput(prunedInput)); measurement.TotalTokens < contextThreshold(assembler.policy) && len(prunes) != 0 {
		updated, err := surfaceStore.PruneTools(surface.Generation, prunes)
		if err != nil {
			return core.ModelStepInput{}, activities, err
		}
		input, err := assembler.materialize(sessionID, systemPrompt, tools, facts, updated, messages)
		return input, append(activities, core.ContextActivity{Kind: core.ContextOverflowRetry, Message: "Provider 报告上下文超限，已裁剪工具结果后重试。"}), err
	}
	// The provider has rejected this exact surface, so an estimate below the
	// local threshold is not sufficient evidence that replaying it will work.
	// Force a checkpoint when no tool result can be pruned.
	if assembler.summarizer == nil {
		return core.ModelStepInput{}, append(activities, core.ContextActivity{Kind: core.ContextOverflowRetry, Message: "Provider 报告上下文超限，但没有可用 checkpoint summarizer。"}), ErrContextPreflight
	}
	checkpointed, checkpointActivities, err := compactor.CheckpointWithValidator(ctx, CompactionRequest{Surface: pruned, Messages: messages, SystemPrompt: systemPrefix, ProjectFacts: factsEnvelope, Tools: tools, Prunes: prunes}, func(candidate core.ContextSurface) error {
		candidateInput, materializeErr := assembler.materialize(sessionID, systemPrompt, tools, facts, candidate, messages)
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
		return core.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	activities = append(activities, core.ContextActivity{Kind: core.ContextOverflowRetry, Message: "Provider 报告上下文超限，已创建 checkpoint 后重试。"})
	input, err := assembler.materialize(sessionID, systemPrompt, tools, facts, checkpointed, messages)
	return input, activities, err
}

func (assembler *ContextAssembler) Prepare(ctx context.Context, sessionID, systemPrompt string, tools []core.Tool, facts string) (core.ModelStepInput, []core.ContextActivity, error) {
	if assembler == nil || assembler.source == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("context assembler dependencies are incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conversation, err := assembler.source.Conversation(sessionID)
	if err != nil {
		return core.ModelStepInput{}, nil, err
	}
	if conversation == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("session conversation is unavailable")
	}
	if !assembler.policy.Enabled() {
		return core.ModelStepInput{
			SessionID:    sessionID,
			Messages:     cloneContextMessageSlice(conversation),
			SystemPrompt: systemPrompt,
			ProjectFacts: facts,
			Tools:        append([]core.Tool(nil), tools...),
		}, nil, nil
	}
	if assembler.meter == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("context assembler meter is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	systemPrefix := llm.SystemInstructionPrefix(systemPrompt)
	surfaceStore, err := assembler.source.ContextSurface(sessionID)
	if err != nil {
		return core.ModelStepInput{}, nil, err
	}
	if surfaceStore == nil {
		return core.ModelStepInput{}, nil, fmt.Errorf("session context surface is unavailable")
	}
	surface, conversationMessages, err := surfaceStore.SnapshotWithConversation()
	if err != nil {
		return core.ModelStepInput{}, nil, err
	}
	messages := numberedMessages(conversationMessages)
	factsEnvelope := llm.ProjectFactsEnvelope(facts)
	activities := make([]core.ContextActivity, 0, 2)
	input, err := assembler.materialize(sessionID, systemPrompt, tools, facts, surface, messages)
	if err != nil {
		return core.ModelStepInput{}, activities, err
	}
	measurement := assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens < contextThreshold(assembler.policy) {
		return input, activities, nil
	}
	fixed := assembler.meter.Measure(Request{SystemPrompt: systemPrefix, ProjectFacts: factsEnvelope, Tools: tools})
	if fixed.TotalTokens >= contextThreshold(assembler.policy) {
		return core.ModelStepInput{}, append(activities, rejectedContextActivity("系统提示词、工具或项目事实本身超过上下文预算。")), ErrContextPreflight
	}
	compactor := NewContextCompactor(assembler.policy, surfaceStore, assembler.meter, assembler.summarizer)
	pruned, prunes, pruneActivities, err := compactor.PreviewPrune(ctx, CompactionRequest{Surface: surface, Messages: messages, SystemPrompt: systemPrefix, ProjectFacts: factsEnvelope, Tools: tools})
	activities = append(activities, pruneActivities...)
	if err != nil {
		return core.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	input, err = assembler.materialize(sessionID, systemPrompt, tools, facts, pruned, messages)
	if err != nil {
		return core.ModelStepInput{}, activities, err
	}
	measurement = assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens < contextThreshold(assembler.policy) {
		if len(prunes) != 0 {
			if _, err := surfaceStore.PruneTools(surface.Generation, prunes); err != nil {
				return core.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
			}
		}
		return input, activities, nil
	}
	if assembler.summarizer == nil {
		return core.ModelStepInput{}, append(activities, rejectedContextActivity("无法在没有 checkpoint summarizer 的情况下压缩上下文。")), ErrContextPreflight
	}
	checkpointRequest := CompactionRequest{Surface: pruned, Messages: messages, SystemPrompt: systemPrefix, ProjectFacts: factsEnvelope, Tools: tools, Prunes: prunes}
	checkpointed, checkpointActivities, err := compactor.CheckpointWithValidator(ctx, checkpointRequest, func(candidate core.ContextSurface) error {
		candidateInput, err := assembler.materialize(sessionID, systemPrompt, tools, facts, candidate, messages)
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
		return core.ModelStepInput{}, append(activities, rejectedContextActivity(err.Error())), err
	}
	input, err = assembler.materialize(sessionID, systemPrompt, tools, facts, checkpointed, messages)
	if err != nil {
		return core.ModelStepInput{}, activities, err
	}
	measurement = assembler.meter.Measure(contextRequestFromInput(input))
	if measurement.TotalTokens >= contextThreshold(assembler.policy) {
		return core.ModelStepInput{}, append(activities, rejectedContextActivity("checkpoint 后上下文仍超过预算。")), ErrContextPreflight
	}
	return input, activities, nil
}

func (assembler *ContextAssembler) materialize(sessionID, systemPrompt string, tools []core.Tool, facts string, surface core.ContextSurface, messages map[int]core.Message) (core.ModelStepInput, error) {
	result := core.ModelStepInput{
		SessionID:       sessionID,
		SystemPrompt:    systemPrompt,
		ProjectFacts:    facts,
		Tools:           append([]core.Tool(nil), tools...),
		ContextWindow:   assembler.policy.ContextWindow,
		SurfaceNodes:    append([]core.SurfaceNode(nil), surface.Nodes...),
		SurfaceMessages: cloneContextMessages(messages),
	}
	for _, node := range surface.Nodes {
		switch node.Kind {
		case core.SurfaceNodeCheckpoint:
			result.Messages = append(result.Messages, core.Message{Role: core.RoleUser, Content: node.Content})
		case core.SurfaceNodeSource, core.SurfaceNodePrunedTool:
			if node.SourceStartSeq != node.SourceEndSeq {
				return core.ModelStepInput{}, fmt.Errorf("surface node %q has a non-materializable source range", node.ID)
			}
			message, found := messages[node.SourceStartSeq]
			if !found {
				return core.ModelStepInput{}, fmt.Errorf("surface source message #%d is unavailable", node.SourceStartSeq)
			}
			message = core.CloneMessage(message)
			if node.Kind == core.SurfaceNodePrunedTool {
				if message.Role != core.RoleTool {
					return core.ModelStepInput{}, fmt.Errorf("pruned surface node %q does not reference a tool result", node.ID)
				}
				message.Content = node.Content
			}
			result.Messages = append(result.Messages, message)
		default:
			return core.ModelStepInput{}, fmt.Errorf("surface node %q has unknown kind %q", node.ID, node.Kind)
		}
	}
	return result, nil
}

func contextRequestFromInput(input core.ModelStepInput) Request {
	nodes := append([]core.SurfaceNode(nil), input.SurfaceNodes...)
	messages := cloneContextMessages(input.SurfaceMessages)
	if len(nodes) == 0 {
		nodes = make([]core.SurfaceNode, 0, len(input.Messages))
		messages = make(map[int]core.Message, len(input.Messages))
		for index, message := range input.Messages {
			sequence := index + 1
			nodes = append(nodes, core.SurfaceNode{Kind: core.SurfaceNodeSource, SourceStartSeq: sequence, SourceEndSeq: sequence})
			messages[sequence] = message
		}
	}
	return Request{SystemPrompt: llm.SystemInstructionPrefix(input.SystemPrompt), ProjectFacts: llm.ProjectFactsEnvelope(input.ProjectFacts), Tools: input.Tools, Nodes: nodes, Messages: messages}
}

func numberedMessages(messages []core.Message) map[int]core.Message {
	result := make(map[int]core.Message, len(messages))
	for index, message := range messages {
		result[index+1] = message
	}
	return result
}

func cloneContextMessageSlice(messages []core.Message) []core.Message {
	cloned := make([]core.Message, len(messages))
	for index, message := range messages {
		cloned[index] = core.CloneMessage(message)
	}
	return cloned
}

func rejectedContextActivity(message string) core.ContextActivity {
	return core.ContextActivity{Kind: core.ContextRequestRejected, Message: message}
}

// RequestFromInput converts a model input into the source-aware representation
// used by the deterministic context meter.
func RequestFromInput(input core.ModelStepInput) Request { return contextRequestFromInput(input) }
