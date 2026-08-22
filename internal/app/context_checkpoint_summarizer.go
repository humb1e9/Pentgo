package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"pentgo/internal/adapters/llm"
	"pentgo/internal/agent"
	"pentgo/internal/config"

	"github.com/cloudwego/eino/components/model"
)

// CheckpointModelFactory creates the short-lived tool-free model used only for
// an internal checkpoint request.
type CheckpointModelFactory func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error)

// modelCheckpointSummarizer performs one independent provider request. It
// never exposes tools, so checkpointing cannot trigger a host tool call.
type modelCheckpointSummarizer struct {
	newModel CheckpointModelFactory
	config   config.AgentConfig
}

// NewModelCheckpointSummarizer returns a production summarizer that lazily
// creates the configured checkpoint model only when compaction needs one.
func NewModelCheckpointSummarizer(factory CheckpointModelFactory, configuration config.AgentConfig) CheckpointSummarizer {
	if factory == nil {
		return nil
	}
	return &modelCheckpointSummarizer{newModel: factory, config: configuration}
}

func (summarizer *modelCheckpointSummarizer) Summarize(ctx context.Context, input agent.CheckpointInput) (agent.CheckpointOutput, error) {
	if summarizer == nil || summarizer.newModel == nil {
		return agent.CheckpointOutput{}, fmt.Errorf("checkpoint model factory is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	configuration, err := checkpointAgentConfig(summarizer.config)
	if err != nil {
		return agent.CheckpointOutput{}, err
	}
	chatModel, err := summarizer.newModel(ctx, configuration)
	if err != nil {
		return agent.CheckpointOutput{}, err
	}
	stepper, err := llm.NewEngine(ctx, chatModel, nil)
	if err != nil {
		return agent.CheckpointOutput{}, err
	}
	stream, err := stepper.StreamStep(ctx, agent.ModelStepInput{
		SystemPrompt: input.SystemPrompt,
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: checkpointSourcePrompt(input),
		}},
	})
	if err != nil {
		return agent.CheckpointOutput{}, err
	}
	var final *agent.Message
	for event := range stream {
		if final != nil {
			if event.Err != nil {
				return agent.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted error after final: %w", event.Err)
			}
			if event.Final != nil {
				return agent.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted multiple final messages")
			}
			if event.Delta.Role != "" || event.Delta.Content != "" || len(event.Delta.ToolCalls) != 0 {
				return agent.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted output after final")
			}
			continue
		}
		if event.Err != nil {
			return agent.CheckpointOutput{}, event.Err
		}
		if event.Final == nil {
			continue
		}
		copy := *event.Final
		final = &copy
	}
	if final == nil || strings.TrimSpace(final.Content) == "" || len(final.ToolCalls) != 0 {
		return agent.CheckpointOutput{}, fmt.Errorf("checkpoint model returned no text-only summary")
	}
	return agent.CheckpointOutput{Text: strings.TrimSpace(final.Content)}, nil
}

func checkpointAgentConfig(configuration config.AgentConfig) (config.AgentConfig, error) {
	result := configuration
	policy := configuration.Context.Effective()
	provider := strings.TrimSpace(policy.CheckpointProvider)
	if provider != "" {
		result.Provider = provider
	}
	if strings.TrimSpace(policy.CheckpointModel) == "" {
		return result, nil
	}
	switch result.Provider {
	case "openai":
		result.OpenAI.Model = policy.CheckpointModel
	case "anthropic":
		result.Anthropic.Model = policy.CheckpointModel
	default:
		return config.AgentConfig{}, fmt.Errorf("unsupported checkpoint provider: %s", result.Provider)
	}
	return result, nil
}

func checkpointSourcePrompt(input agent.CheckpointInput) string {
	var builder strings.Builder
	builder.WriteString(input.Prompt)
	builder.WriteString("\n\n<available-tool-schemas>\n")
	for _, tool := range input.Tools {
		if tool == nil {
			continue
		}
		schema := map[string]any{"name": tool.Name(), "description": tool.Description()}
		if provider, ok := tool.(agent.ToolSchemaProvider); ok {
			schema["parameters"] = provider.InputSchema()
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		builder.Write(encoded)
		builder.WriteByte('\n')
	}
	builder.WriteString("</available-tool-schemas>\n\n<untrusted-checkpoint-source>\n")
	for _, node := range input.Nodes {
		fmt.Fprintf(&builder, "<surface-node kind=%q source_start=%d source_end=%d>\n", node.Kind, node.SourceStartSeq, node.SourceEndSeq)
		if node.Kind == agent.SurfaceNodeCheckpoint {
			encoded, err := json.Marshal(node.Content)
			if err != nil {
				continue
			}
			fmt.Fprintf(&builder, "<checkpoint-text>%s</checkpoint-text>\n", encoded)
		} else {
			for sequence := node.SourceStartSeq; sequence <= node.SourceEndSeq; sequence++ {
				message, ok := input.Messages[sequence]
				if !ok {
					continue
				}
				encoded, err := json.Marshal(message)
				if err != nil {
					continue
				}
				fmt.Fprintf(&builder, "<message sequence=%d>%s</message>\n", sequence, encoded)
			}
		}
		builder.WriteString("</surface-node>\n")
	}
	builder.WriteString("</untrusted-checkpoint-source>")
	return builder.String()
}
