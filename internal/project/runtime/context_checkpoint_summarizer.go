package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"pentgo/internal/core"
	"pentgo/internal/model"
	llm "pentgo/internal/model"

	einomodel "github.com/cloudwego/eino/components/model"
)

// CheckpointModelFactory creates the short-lived tool-free model used only for
// an internal checkpoint request.
type CheckpointModelFactory func(context.Context, model.Config) (einomodel.ToolCallingChatModel, error)

// modelCheckpointSummarizer performs one independent provider request. It
// never exposes tools, so checkpointing cannot trigger a host tool call.
type modelCheckpointSummarizer struct {
	newModel CheckpointModelFactory
	config   model.Config
}

// NewModelCheckpointSummarizer returns a production summarizer that lazily
// creates the configured checkpoint model only when compaction needs one.
func NewModelCheckpointSummarizer(factory CheckpointModelFactory, configuration model.Config) CheckpointSummarizer {
	if factory == nil {
		return nil
	}
	return &modelCheckpointSummarizer{newModel: factory, config: configuration}
}

func (summarizer *modelCheckpointSummarizer) Summarize(ctx context.Context, input core.CheckpointInput) (core.CheckpointOutput, error) {
	if summarizer == nil || summarizer.newModel == nil {
		return core.CheckpointOutput{}, fmt.Errorf("checkpoint model factory is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	chatModel, err := summarizer.newModel(ctx, summarizer.config)
	if err != nil {
		return core.CheckpointOutput{}, err
	}
	stepper, err := llm.NewEngine(ctx, chatModel, nil)
	if err != nil {
		return core.CheckpointOutput{}, err
	}
	stream, err := stepper.StreamStep(ctx, core.ModelStepInput{
		SystemPrompt:    input.SystemPrompt,
		MaxOutputTokens: input.OutputTokenCap,
		Messages: []core.Message{{
			Role:    core.RoleUser,
			Content: checkpointSourcePrompt(input),
		}},
	})
	if err != nil {
		return core.CheckpointOutput{}, err
	}
	var final *core.Message
	truncated := false
	for event := range stream {
		if final != nil {
			if event.Err != nil {
				return core.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted error after final: %w", event.Err)
			}
			if event.Final != nil {
				return core.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted multiple final messages")
			}
			if event.Delta.Role != "" || event.Delta.Content != "" || len(event.Delta.ToolCalls) != 0 {
				return core.CheckpointOutput{}, fmt.Errorf("checkpoint model emitted output after final")
			}
			continue
		}
		if event.Err != nil {
			return core.CheckpointOutput{}, event.Err
		}
		if event.Final == nil {
			continue
		}
		copy := *event.Final
		final = &copy
		truncated = outputWasTruncated(event.FinishReason)
	}
	if final == nil || strings.TrimSpace(final.Content) == "" || len(final.ToolCalls) != 0 {
		return core.CheckpointOutput{}, fmt.Errorf("checkpoint model returned no text-only summary")
	}
	return core.CheckpointOutput{Text: strings.TrimSpace(final.Content), Truncated: truncated}, nil
}

func outputWasTruncated(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens", "token_limit", "max_tokens_reached":
		return true
	default:
		return false
	}
}

func checkpointSourcePrompt(input core.CheckpointInput) string {
	var builder strings.Builder
	builder.WriteString(input.Prompt)
	builder.WriteString("\n\n<available-tool-schemas>\n")
	for _, tool := range input.Tools {
		if tool == nil {
			continue
		}
		schema := map[string]any{"name": tool.Name(), "description": tool.Description()}
		if provider, ok := tool.(core.ToolSchemaProvider); ok {
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
		if node.Kind == core.SurfaceNodeCheckpoint {
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
