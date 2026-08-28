package context

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"pentgo/internal/core"
)

const (
	estimatedCharactersPerToken = 4
	messageTokenOverhead        = 4
	toolSchemaTokenOverhead     = 8
)

func EstimateTextTokens(value string) int {
	asciiRunes, nonASCIITokens := 0, 0
	for _, runeValue := range value {
		if runeValue < utf8.RuneSelf {
			asciiRunes++
		} else {
			nonASCIITokens++
		}
	}
	return nonASCIITokens + (asciiRunes+estimatedCharactersPerToken-1)/estimatedCharactersPerToken
}

func EstimateMessageTokens(messages []core.Message) int {
	total := 0
	for _, message := range messages {
		parts := []string{message.Role, message.Content, message.ReasoningContent, message.ToolCallID, message.ToolName}
		for _, call := range message.ToolCalls {
			encoded, _ := json.Marshal(call.Arguments)
			parts = append(parts, call.ID, call.Name, call.RawArguments, string(encoded))
		}
		total += EstimateTextTokens(strings.Join(parts, "\n")) + messageTokenOverhead
	}
	return total
}

func EstimateToolTokens(tools []core.Tool) int {
	total := 0
	for _, projectTool := range tools {
		if projectTool == nil {
			continue
		}
		schema := map[string]any{"name": projectTool.Name(), "description": projectTool.Description(), "parameters": map[string]any{"type": "object"}}
		if provider, ok := projectTool.(core.ToolSchemaProvider); ok && provider.InputSchema() != nil {
			schema["parameters"] = provider.InputSchema()
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			encoded = []byte(fmt.Sprintf("%s %s", projectTool.Name(), projectTool.Description()))
		}
		total += EstimateTextTokens(string(encoded)) + toolSchemaTokenOverhead
	}
	return total
}
