package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// configuredChatModel is the protocol boundary for OpenAI-compatible providers.
// The upstream adapter owns standard Chat Completions fields; this wrapper only
// normalizes an explicitly configured provider response extension into Eino's
// provider-neutral ReasoningContent field.
type configuredChatModel struct {
	base                  model.ToolCallingChatModel
	responsePointer       string
	streamResponsePointer string
}

func newConfiguredChatModel(base model.ToolCallingChatModel, responsePointer, streamResponsePointer string) model.ToolCallingChatModel {
	if base == nil || (strings.TrimSpace(responsePointer) == "" && strings.TrimSpace(streamResponsePointer) == "") {
		return base
	}
	return &configuredChatModel{
		base:                  base,
		responsePointer:       strings.TrimSpace(responsePointer),
		streamResponsePointer: strings.TrimSpace(streamResponsePointer),
	}
}

func (model *configuredChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if model == nil || model.base == nil {
		return nil, fmt.Errorf("configured chat model is nil")
	}
	if model.responsePointer != "" {
		opts = append(opts, einoopenai.WithResponseMessageModifier(func(_ context.Context, message *schema.Message, raw []byte) (*schema.Message, error) {
			if reasoning, ok := jsonPointerString(raw, model.responsePointer); ok && message.ReasoningContent == "" {
				message.ReasoningContent = reasoning
			}
			return message, nil
		}))
	}
	return model.base.Generate(ctx, input, opts...)
}

func (model *configuredChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if model == nil || model.base == nil {
		return nil, fmt.Errorf("configured chat model is nil")
	}
	if model.streamResponsePointer != "" {
		opts = append(opts, einoopenai.WithResponseChunkMessageModifier(func(_ context.Context, message *schema.Message, raw []byte, _ bool) (*schema.Message, error) {
			if message != nil {
				if reasoning, ok := jsonPointerString(raw, model.streamResponsePointer); ok && message.ReasoningContent == "" {
					message.ReasoningContent = reasoning
				}
			}
			return message, nil
		}))
	}
	return model.base.Stream(ctx, input, opts...)
}

func (model *configuredChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if model == nil || model.base == nil {
		return nil, fmt.Errorf("configured chat model is nil")
	}
	base, err := model.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return newConfiguredChatModel(base, model.responsePointer, model.streamResponsePointer), nil
}

// jsonPointerString reads a string value from a JSON response using RFC 6901
// JSON Pointer notation. It deliberately accepts only strings: reasoning data
// is model text, and silently stringifying arbitrary JSON would corrupt it.
func jsonPointerString(raw []byte, pointer string) (string, bool) {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || len(raw) == 0 || (pointer != "" && pointer[0] != '/') {
		return "", false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	for _, token := range strings.Split(pointer[1:], "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[token]
			if !ok {
				return "", false
			}
		case []any:
			var index int
			if _, err := fmt.Sscanf(token, "%d", &index); err != nil || index < 0 || index >= len(current) {
				return "", false
			}
			value = current[index]
		default:
			return "", false
		}
	}
	result, ok := value.(string)
	return result, ok && result != ""
}

var _ model.ToolCallingChatModel = (*configuredChatModel)(nil)
