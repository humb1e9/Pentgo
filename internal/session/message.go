package session

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RawArguments string         `json:"raw_arguments,omitempty"`
}

// Message is one model-visible conversation message.
type Message struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	ToolArguments    map[string]any `json:"tool_arguments,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
}

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// CloneMessage returns an independent copy of a model message and its JSON-like
// tool arguments.
func CloneMessage(message Message) Message {
	cloned := message
	cloned.ToolArguments = CloneArguments(message.ToolArguments)
	if message.ToolCalls != nil {
		cloned.ToolCalls = make([]ToolCall, len(message.ToolCalls))
		for index, call := range message.ToolCalls {
			cloned.ToolCalls[index] = call
			cloned.ToolCalls[index].Arguments = CloneArguments(call.Arguments)
		}
	}
	return cloned
}

// CloneArguments returns an independent copy of JSON-like tool arguments.
func CloneArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return CloneArguments(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}
