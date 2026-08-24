package core

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
