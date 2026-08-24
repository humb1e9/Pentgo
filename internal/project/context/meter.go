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

// Request is the provider-neutral, assembled request measured before a
// model step. Source nodes resolve their immutable conversation message from
// Messages; replacement nodes use their content directly.
type Request struct {
	SystemPrompt string
	Tools        []core.Tool
	ProjectFacts string
	Nodes        []core.SurfaceNode
	Messages     map[int]core.Message
}

// Meter deterministically estimates model input tokens.
type Meter interface {
	Measure(Request) core.ContextMeasurement
}

type contextMeter struct{}

// NewMeter returns the deterministic Phase 1 context meter.
func NewMeter() *contextMeter { return &contextMeter{} }

// Measure returns an independent value snapshot for the assembled request.
func (meter *contextMeter) Measure(request Request) core.ContextMeasurement {
	// The provider receives system prompt and project facts as one system
	// message, so they are measured together to avoid rounding drift.
	systemAndProjectFactsTokens := EstimateTextTokens(providerSystemMessage(request))
	measurement := core.ContextMeasurement{
		SystemTokens:     systemAndProjectFactsTokens,
		ToolSchemaTokens: measureTools(request.Tools),
		SurfaceTokens:    measureSurface(request.Nodes, request.Messages),
	}
	measurement.TotalTokens = systemAndProjectFactsTokens + measurement.ToolSchemaTokens + measurement.SurfaceTokens
	return measurement
}

func measureTools(tools []core.Tool) int {
	total := 0
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		schema := map[string]any{"name": tool.Name(), "description": tool.Description(), "parameters": map[string]any{"type": "object"}}
		if source, ok := tool.(core.ToolSchemaProvider); ok && source.InputSchema() != nil {
			schema["parameters"] = source.InputSchema()
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			encoded = []byte(fmt.Sprintf("%s %s", tool.Name(), tool.Description()))
		}
		total += EstimateTextTokens(string(encoded)) + toolSchemaTokenOverhead
	}
	return total
}

func measureSurface(nodes []core.SurfaceNode, messages map[int]core.Message) int {
	total := 0
	for _, node := range nodes {
		content := node.Content
		if node.Kind == core.SurfaceNodeSource {
			message, ok := messages[node.SourceStartSeq]
			if !ok {
				continue
			}
			content = RenderMeasuredMessage(message)
		}
		total += EstimateTextTokens(content) + messageTokenOverhead
	}
	return total
}

func RenderMeasuredMessage(message core.Message) string {
	parts := []string{message.Role, message.Content, message.ReasoningContent, message.ToolCallID, message.ToolName}
	for _, call := range message.ToolCalls {
		encoded, _ := json.Marshal(call.Arguments)
		parts = append(parts, call.ID, call.Name, call.RawArguments, string(encoded))
	}
	return strings.Join(parts, "\n")
}

func EstimateTextTokens(value string) int {
	if value == "" {
		return 0
	}
	return (utf8.RuneCountInString(value) + estimatedCharactersPerToken - 1) / estimatedCharactersPerToken
}

func providerSystemMessage(request Request) string {
	return request.SystemPrompt + request.ProjectFacts
}
