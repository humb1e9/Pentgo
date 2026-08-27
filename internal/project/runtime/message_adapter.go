package runtime

import (
	"encoding/json"

	"pentgo/internal/core"

	"github.com/cloudwego/eino/schema"
)

// toEinoMessage converts a provider-neutral message to Eino's message type.
func toEinoMessage(message core.Message) *schema.Message {
	switch message.Role {
	case core.RoleSystem:
		converted := schema.SystemMessage(message.Content)
		converted.Extra = core.CloneArguments(message.ToolArguments)
		return converted
	case core.RoleUser:
		converted := schema.UserMessage(message.Content)
		converted.Extra = core.CloneArguments(message.ToolArguments)
		return converted
	case core.RoleTool:
		converted := schema.ToolMessage(message.Content, message.ToolCallID, schema.WithToolName(message.ToolName))
		converted.Extra = core.CloneArguments(message.ToolArguments)
		return converted
	case core.RoleAssistant:
		calls := make([]schema.ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			arguments := call.RawArguments
			if arguments == "" {
				data, _ := json.Marshal(call.Arguments)
				arguments = string(data)
			}
			calls = append(calls, schema.ToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: schema.FunctionCall{Name: call.Name, Arguments: arguments},
			})
		}
		converted := schema.AssistantMessage(message.Content, calls)
		converted.ReasoningContent = message.ReasoningContent
		converted.Extra = core.CloneArguments(message.ToolArguments)
		return converted
	default:
		return &schema.Message{
			Role:             schema.RoleType(message.Role),
			Content:          message.Content,
			ReasoningContent: message.ReasoningContent,
			ToolCallID:       message.ToolCallID,
			ToolName:         message.ToolName,
			Extra:            core.CloneArguments(message.ToolArguments),
		}
	}
}

func toEinoMessages(messages []core.Message) []*schema.Message {
	converted := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, toEinoMessage(message))
	}
	return converted
}

// fromEinoMessage converts Eino messages without discarding raw tool arguments.
func fromEinoMessage(message *schema.Message) core.Message {
	if message == nil {
		return core.Message{}
	}
	converted := core.Message{
		Role:             string(message.Role),
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		ToolCallID:       message.ToolCallID,
		ToolName:         message.ToolName,
		ToolArguments:    core.CloneArguments(message.Extra),
	}
	for _, call := range message.ToolCalls {
		var arguments map[string]any
		_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
		converted.ToolCalls = append(converted.ToolCalls, core.ToolCall{
			ID:           call.ID,
			Name:         call.Function.Name,
			Arguments:    arguments,
			RawArguments: call.Function.Arguments,
		})
	}
	if converted.Role == "" {
		converted.Role = core.RoleAssistant
	}
	return converted
}

func fromEinoMessages(messages []*schema.Message) []core.Message {
	converted := make([]core.Message, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, fromEinoMessage(message))
	}
	return converted
}
