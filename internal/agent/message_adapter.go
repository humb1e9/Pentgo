package agent

import (
	"encoding/json"
	"pentgo/internal/session"

	"github.com/cloudwego/eino/schema"
)

// toEinoMessage converts a provider-neutral message to Eino's message type.
func toEinoMessage(message session.Message) *schema.Message {
	switch message.Role {
	case session.RoleSystem:
		converted := schema.SystemMessage(message.Content)
		converted.Extra = session.CloneArguments(message.ToolArguments)
		return converted
	case session.RoleUser:
		converted := schema.UserMessage(message.Content)
		converted.Extra = session.CloneArguments(message.ToolArguments)
		return converted
	case session.RoleTool:
		converted := schema.ToolMessage(message.Content, message.ToolCallID, schema.WithToolName(message.ToolName))
		converted.Extra = session.CloneArguments(message.ToolArguments)
		return converted
	case session.RoleAssistant:
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
		converted.Extra = session.CloneArguments(message.ToolArguments)
		return converted
	default:
		return &schema.Message{
			Role:             schema.RoleType(message.Role),
			Content:          message.Content,
			ReasoningContent: message.ReasoningContent,
			ToolCallID:       message.ToolCallID,
			ToolName:         message.ToolName,
			Extra:            session.CloneArguments(message.ToolArguments),
		}
	}
}

func toEinoMessages(messages []session.Message) []*schema.Message {
	converted := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, toEinoMessage(message))
	}
	return converted
}

// fromEinoMessage converts Eino messages without discarding raw tool arguments.
func fromEinoMessage(message *schema.Message) session.Message {
	if message == nil {
		return session.Message{}
	}
	converted := session.Message{
		Role:             string(message.Role),
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		ToolCallID:       message.ToolCallID,
		ToolName:         message.ToolName,
		ToolArguments:    session.CloneArguments(message.Extra),
	}
	for _, call := range message.ToolCalls {
		var arguments map[string]any
		_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
		converted.ToolCalls = append(converted.ToolCalls, session.ToolCall{
			ID:           call.ID,
			Name:         call.Function.Name,
			Arguments:    arguments,
			RawArguments: call.Function.Arguments,
		})
	}
	if converted.Role == "" {
		converted.Role = session.RoleAssistant
	}
	return converted
}

func fromEinoMessages(messages []*schema.Message) []session.Message {
	converted := make([]session.Message, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, fromEinoMessage(message))
	}
	return converted
}
