package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"pentgo/internal/project/turn"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// evidenceMiddleware records every tool result before exposing it to the model.
type evidenceMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	store *turn.EvidenceStore
}

// NewEvidenceMiddleware constructs the evidence-first tool middleware.
func NewEvidenceMiddleware(store *turn.EvidenceStore) adk.ChatModelAgentMiddleware {
	return &evidenceMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store}
}

func newEvidenceMiddleware(store *turn.EvidenceStore) *evidenceMiddleware {
	return &evidenceMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, store: store}
}

func (middleware *evidenceMiddleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, toolContext *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	if middleware == nil || middleware.store == nil {
		return nil, fmt.Errorf("evidence store is unavailable")
	}
	if endpoint == nil {
		return nil, fmt.Errorf("tool endpoint is nil")
	}
	name := toolName(toolContext)
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		arguments, err := decodeObjectArguments(argumentsInJSON)
		if err != nil {
			return "", fmt.Errorf("invalid arguments for tool %s: %w", name, err)
		}
		output, invokeErr := endpoint(ctx, argumentsInJSON, opts...)
		return middleware.record(ctx, name, arguments, output, invokeErr)
	}, nil
}

func (middleware *evidenceMiddleware) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, toolContext *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	if middleware == nil || middleware.store == nil {
		return nil, fmt.Errorf("evidence store is unavailable")
	}
	if endpoint == nil {
		return nil, fmt.Errorf("tool endpoint is nil")
	}
	name := toolName(toolContext)
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		arguments, err := decodeObjectArguments(argumentsInJSON)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments for tool %s: %w", name, err)
		}
		stream, invokeErr := endpoint(ctx, argumentsInJSON, opts...)
		if invokeErr != nil {
			output, recordErr := middleware.record(ctx, name, arguments, "", invokeErr)
			if recordErr != nil {
				return nil, recordErr
			}
			return schema.StreamReaderFromArray([]string{output}), nil
		}
		if stream == nil {
			output, recordErr := middleware.record(ctx, name, arguments, "", fmt.Errorf("tool %s returned nil stream", name))
			if recordErr != nil {
				return nil, recordErr
			}
			return schema.StreamReaderFromArray([]string{output}), nil
		}
		defer stream.Close()
		var chunks strings.Builder
		for {
			chunk, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr == io.EOF {
					break
				}
				output, recordErr := middleware.record(ctx, name, arguments, chunks.String(), recvErr)
				if recordErr != nil {
					return nil, recordErr
				}
				return schema.StreamReaderFromArray([]string{output}), nil
			}
			chunks.WriteString(chunk)
		}
		output, recordErr := middleware.record(ctx, name, arguments, chunks.String(), nil)
		if recordErr != nil {
			return nil, recordErr
		}
		return schema.StreamReaderFromArray([]string{output}), nil
	}, nil
}

func (middleware *evidenceMiddleware) record(ctx context.Context, name string, arguments map[string]any, output string, invokeErr error) (string, error) {
	if invokeErr != nil {
		if strings.TrimSpace(output) == "" {
			output = "工具调用失败：" + invokeErr.Error()
		} else {
			output = "工具调用失败：" + invokeErr.Error() + "\n" + output
		}
	}
	record, err := middleware.store.RecordResult(ctx, name, arguments, invokeErr == nil, output)
	if err != nil {
		return "", err
	}
	return record.Output, nil
}

func toolName(toolContext *adk.ToolContext) string {
	if toolContext == nil {
		return ""
	}
	return toolContext.Name
}

func decodeObjectArguments(argumentsInJSON string) (map[string]any, error) {
	var arguments map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &arguments); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	if arguments == nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return arguments, nil
}

var _ adk.ChatModelAgentMiddleware = (*evidenceMiddleware)(nil)
