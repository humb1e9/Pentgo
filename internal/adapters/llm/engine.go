package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"pentgo/internal/agent"

	"github.com/anthropics/anthropic-sdk-go"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

// Engine adapts one provider request into the host-owned ModelStepper contract.
// It has no transcript, tool-execution, or session state: the host assembles a
// request, persists the complete response, and executes requested tools.
type Engine struct {
	model model.ToolCallingChatModel
	tools []agent.Tool
}

// NewEngine validates the default model-visible tools and saves only immutable
// adapter dependencies. Callers may replace the default tools per StreamStep.
func NewEngine(_ context.Context, chatModel model.ToolCallingChatModel, tools []agent.Tool) (*Engine, error) {
	if chatModel == nil {
		return nil, fmt.Errorf("eino chat model is nil")
	}
	if err := validateTools(tools); err != nil {
		return nil, err
	}
	return &Engine{model: chatModel, tools: append([]agent.Tool(nil), tools...)}, nil
}

// StreamStep makes one streamed model request. It binds tool schemas but never
// exposes invokable tools to Eino, so no tool can run inside this adapter.
func (engine *Engine) StreamStep(ctx context.Context, input agent.ModelStepInput) (<-chan agent.ModelStreamEvent, error) {
	if engine == nil || engine.model == nil {
		return nil, fmt.Errorf("eino engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tools := append([]agent.Tool(nil), engine.tools...)
	if input.Tools != nil {
		tools = input.Tools
	}
	if err := validateTools(tools); err != nil {
		return nil, err
	}
	infos, err := toolInfos(tools)
	if err != nil {
		return nil, err
	}
	bound := engine.model
	if len(infos) != 0 {
		bound, err = engine.model.WithTools(infos)
		if err != nil {
			return nil, normalizeContextWindowError(err)
		}
	}

	options := make([]model.Option, 0, 1)
	if input.MaxOutputTokens > 0 {
		options = append(options, model.WithMaxTokens(input.MaxOutputTokens))
	}
	instruction := SystemPrompt(input.SystemPrompt, input.ProjectFacts)
	messages := make([]*schema.Message, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemMessage(instruction))
	messages = append(messages, toSchemaMessages(input.Messages)...)
	reader, err := bound.Stream(ctx, messages, options...)
	if err != nil {
		return nil, normalizeContextWindowError(err)
	}
	if reader == nil {
		return nil, fmt.Errorf("model returned nil stream reader")
	}

	output := make(chan agent.ModelStreamEvent, 16)
	go func() {
		defer close(output)
		defer reader.Close()

		chunks := make([]*schema.Message, 0, 8)
		for {
			chunk, recvErr := reader.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				sendStreamEvent(ctx, output, agent.ModelStreamEvent{Err: normalizeContextWindowError(recvErr)})
				return
			}
			if chunk == nil {
				continue
			}
			chunks = append(chunks, chunk)
			if !sendStreamEvent(ctx, output, agent.ModelStreamEvent{Delta: fromSchemaMessage(chunk)}) {
				return
			}
		}
		if len(chunks) == 0 {
			sendStreamEvent(ctx, output, agent.ModelStreamEvent{Err: fmt.Errorf("model stream ended without a message")})
			return
		}
		complete, concatErr := schema.ConcatMessages(chunks)
		if concatErr != nil {
			sendStreamEvent(ctx, output, agent.ModelStreamEvent{Err: concatErr})
			return
		}
		if complete == nil {
			sendStreamEvent(ctx, output, agent.ModelStreamEvent{Err: fmt.Errorf("model stream concatenated to nil message")})
			return
		}
		final := fromSchemaMessage(complete)
		finishReason := ""
		if complete.ResponseMeta != nil {
			finishReason = complete.ResponseMeta.FinishReason
		}
		sendStreamEvent(ctx, output, agent.ModelStreamEvent{Final: &final, FinishReason: finishReason})
	}()
	return output, nil
}

func sendStreamEvent(ctx context.Context, output chan<- agent.ModelStreamEvent, event agent.ModelStreamEvent) bool {
	select {
	case output <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// normalizeContextWindowError maps only explicit OpenAI-compatible context
// overflow codes to the retryable host sentinel. Other provider failures keep
// their original type and message for diagnostics and normal error handling.
func normalizeContextWindowError(err error) error {
	if err == nil || errors.Is(err, agent.ErrContextWindowExceeded) {
		return err
	}
	var apiErr *einoopenai.APIError
	if errors.As(err, &apiErr) && apiErr != nil && (isContextWindowCode(apiErr.Code) || isContextWindowCode(apiErr.Type)) {
		return fmt.Errorf("%w: %v", agent.ErrContextWindowExceeded, err)
	}
	if isAnthropicContextWindowError(err) {
		return fmt.Errorf("%w: %v", agent.ErrContextWindowExceeded, err)
	}
	return err
}

// isAnthropicContextWindowError recognizes only an error produced by the
// Anthropic SDK, with its invalid-request type and documented prompt-limit
// diagnostic. Arbitrary RawJSON providers and configuration errors never enter
// the retry path.
func isAnthropicContextWindowError(err error) bool {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr == nil || apiErr.Type() != anthropic.ErrorTypeInvalidRequestError {
		return false
	}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(apiErr.RawJSON()), &envelope) != nil || envelope.Error.Type != "invalid_request_error" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(envelope.Error.Message)), "prompt is too long:")
}

func isContextWindowCode(value any) bool {
	code, ok := value.(string)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "context_length_exceeded", "context_window_exceeded", "max_context_length_exceeded":
		return true
	default:
		return false
	}
}

// validateTools rejects nil tools and duplicate names before model binding.
func validateTools(portsTools []agent.Tool) error {
	seen := make(map[string]bool, len(portsTools))
	for _, port := range portsTools {
		if port == nil {
			return fmt.Errorf("Eino tool is nil")
		}
		name := strings.TrimSpace(port.Name())
		if name == "" {
			return fmt.Errorf("Eino tool name is empty")
		}
		if seen[name] {
			return fmt.Errorf("Eino tool name collision: %s", name)
		}
		seen[name] = true
	}
	return nil
}

// toolInfos converts provider-neutral tool schemas to Eino model metadata.
func toolInfos(portsTools []agent.Tool) ([]*schema.ToolInfo, error) {
	result := make([]*schema.ToolInfo, 0, len(portsTools))
	for _, port := range portsTools {
		info, err := toolInfo(port)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	return result, nil
}

// toolInfo preserves the provided JSON Schema; ordinary tools use an object
// schema, ensuring every provider-neutral tool remains model-callable.
func toolInfo(port agent.Tool) (*schema.ToolInfo, error) {
	inputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	if provider, ok := port.(agent.ToolSchemaProvider); ok && provider.InputSchema() != nil {
		inputSchema = provider.InputSchema()
	}
	data, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode tool %s schema: %w", port.Name(), err)
	}
	var converted einojsonschema.Schema
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, fmt.Errorf("decode tool %s schema: %w", port.Name(), err)
	}
	return &schema.ToolInfo{Name: port.Name(), Desc: port.Description(), ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&converted)}, nil
}

// toSchemaMessages converts persisted messages into one provider request.
func toSchemaMessages(messages []agent.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case agent.RoleSystem:
			result = append(result, schema.SystemMessage(message.Content))
		case agent.RoleUser:
			result = append(result, schema.UserMessage(message.Content))
		case agent.RoleTool:
			result = append(result, schema.ToolMessage(message.Content, message.ToolCallID, schema.WithToolName(message.ToolName)))
		case agent.RoleAssistant:
			calls := make([]schema.ToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				arguments := call.RawArguments
				if strings.TrimSpace(arguments) == "" {
					data, _ := json.Marshal(call.Arguments)
					arguments = string(data)
				}
				calls = append(calls, schema.ToolCall{ID: call.ID, Function: schema.FunctionCall{Name: call.Name, Arguments: arguments}})
			}
			assistant := schema.AssistantMessage(message.Content, calls)
			assistant.ReasoningContent = message.ReasoningContent
			result = append(result, assistant)
		default:
			result = append(result, &schema.Message{Role: schema.RoleType(message.Role), Content: message.Content})
		}
	}
	return result
}

// fromSchemaMessage preserves tool IDs, raw malformed JSON arguments, and
// reasoning content so transcript persistence stays faithful to the provider.
func fromSchemaMessage(message *schema.Message) agent.Message {
	converted := agent.Message{Role: string(message.Role), Content: message.Content, ReasoningContent: message.ReasoningContent, ToolCallID: message.ToolCallID, ToolName: message.ToolName}
	converted.ToolArguments = cloneMap(message.Extra)
	for _, call := range message.ToolCalls {
		arguments := make(map[string]any)
		_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
		converted.ToolCalls = append(converted.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments, RawArguments: call.Function.Arguments})
	}
	if converted.Role == "" {
		converted.Role = agent.RoleAssistant
	}
	return converted
}

// cloneMap avoids sharing mutable provider parameters with persistent messages.
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

var _ agent.ModelStepper = (*Engine)(nil)
