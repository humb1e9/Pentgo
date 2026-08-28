package context

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"pentgo/internal/session"
	"pentgo/internal/tools"
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

func EstimateMessageTokens(messages []session.Message) int {
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

func EstimateToolTokens(projectTools []tools.Tool) int {
	total := 0
	for _, projectTool := range projectTools {
		if projectTool == nil {
			continue
		}
		schema := map[string]any{"name": projectTool.Name(), "description": projectTool.Description(), "parameters": map[string]any{"type": "object"}}
		if provider, ok := projectTool.(tools.ToolSchemaProvider); ok && provider.InputSchema() != nil {
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
