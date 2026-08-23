package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

const (
	estimatedCharactersPerToken = 4
	messageTokenOverhead        = 4
	toolSchemaTokenOverhead     = 8
)

// ContextRequest is the provider-neutral, assembled request measured before a
// model step. Source nodes resolve their immutable transcript message from
// Messages; replacement nodes use their content directly.
type ContextRequest struct {
	SystemPrompt string
	Tools        []agent.Tool
	FactIndex    string
	Nodes        []agent.SurfaceNode
	Messages     map[int]agent.Message
}

// ContextMeter deterministically estimates model input tokens. Implementations
// may anchor exact provider usage only for an identical normalized envelope.
type ContextMeter interface {
	Measure(ContextRequest) agent.ContextMeasurement
}

type contextMeter struct {
	mu      sync.RWMutex
	anchors map[string]int
}

// NewContextMeter returns the deterministic Phase 1 context meter.
func NewContextMeter() *contextMeter {
	return &contextMeter{anchors: make(map[string]int)}
}

// Measure returns an independent value snapshot for the assembled request.
func (meter *contextMeter) Measure(request ContextRequest) agent.ContextMeasurement {
	// The provider receives system prompt and project facts as one system message.
	// Split its single rounded estimate only for component reporting, so Total is
	// never inflated by rounding each part independently.
	systemTokens := estimateTextTokens(request.SystemPrompt)
	systemAndFactIndexTokens := estimateTextTokens(providerSystemMessage(request))
	measurement := agent.ContextMeasurement{
		SystemTokens:     systemTokens,
		ToolSchemaTokens: measureTools(request.Tools),
		FactIndexTokens:  systemAndFactIndexTokens - systemTokens,
		SurfaceTokens:    measureSurface(request.Nodes, request.Messages),
	}
	measurement.TotalTokens = systemAndFactIndexTokens + measurement.ToolSchemaTokens + measurement.SurfaceTokens
	if meter == nil {
		return measurement
	}
	meter.mu.RLock()
	anchored, ok := meter.anchors[normalizedEnvelope(request)]
	meter.mu.RUnlock()
	if ok && anchored >= 0 {
		measurement.TotalTokens = anchored
	}
	return measurement
}

// RecordProviderUsage associates provider input usage with exactly one
// normalized request envelope. All other requests retain deterministic values.
func (meter *contextMeter) RecordProviderUsage(_ string, normalizedEnvelope string, inputTokens int) {
	if meter == nil || inputTokens < 0 || normalizedEnvelope == "" {
		return
	}
	meter.mu.Lock()
	meter.anchors[normalizedEnvelope] = inputTokens
	meter.mu.Unlock()
}

func measureTools(tools []agent.Tool) int {
	total := 0
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		schema := map[string]any{"name": tool.Name(), "description": tool.Description(), "parameters": map[string]any{"type": "object"}}
		if source, ok := tool.(agent.ToolSchemaProvider); ok && source.InputSchema() != nil {
			schema["parameters"] = source.InputSchema()
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			encoded = []byte(fmt.Sprintf("%s %s", tool.Name(), tool.Description()))
		}
		total += estimateTextTokens(string(encoded)) + toolSchemaTokenOverhead
	}
	return total
}

func measureSurface(nodes []agent.SurfaceNode, messages map[int]agent.Message) int {
	total := 0
	for _, node := range nodes {
		content := node.Content
		if node.Kind == agent.SurfaceNodeSource {
			message, ok := messages[node.SourceStartSeq]
			if !ok {
				continue
			}
			content = renderMeasuredMessage(message)
		}
		total += estimateTextTokens(content) + messageTokenOverhead
	}
	return total
}

func renderMeasuredMessage(message agent.Message) string {
	parts := []string{message.Role, message.Content, message.ReasoningContent, message.ToolCallID, message.ToolName}
	for _, call := range message.ToolCalls {
		encoded, _ := json.Marshal(call.Arguments)
		parts = append(parts, call.ID, call.Name, call.RawArguments, string(encoded))
	}
	return strings.Join(parts, "\n")
}

func estimateTextTokens(value string) int {
	if value == "" {
		return 0
	}
	return (utf8.RuneCountInString(value) + estimatedCharactersPerToken - 1) / estimatedCharactersPerToken
}

// BlackboardRender is a bounded, rendered project-facts block plus the exact
// displayed and omitted counts used for activity reporting.
type BlackboardRender struct {
	Text      string
	Shown     int
	Omitted   int
	Truncated bool
}

// RenderBoundedBlackboard renders most-recently-updated facts first while
// preserving the persisted fact order for all legacy consumers.
func RenderBoundedBlackboard(board *domain.Blackboard, tokenBudget int) BlackboardRender {
	if board == nil || len(board.Facts) == 0 {
		return BlackboardRender{Text: `<project-facts shown="0" omitted="0" truncated="false">当前没有记录项目事实。</project-facts>`}
	}
	type orderedFact struct {
		fact  domain.Fact
		index int
	}
	facts := make([]orderedFact, 0, len(board.Facts))
	for index, fact := range board.Facts {
		facts = append(facts, orderedFact{fact: fact, index: index})
	}
	sort.SliceStable(facts, func(left, right int) bool {
		leftAt, rightAt := facts[left].fact.UpdatedAt, facts[right].fact.UpdatedAt
		if leftAt.IsZero() {
			leftAt = facts[left].fact.At
		}
		if rightAt.IsZero() {
			rightAt = facts[right].fact.At
		}
		if !leftAt.Equal(rightAt) {
			return leftAt.After(rightAt)
		}
		if facts[left].fact.Key != facts[right].fact.Key {
			return facts[left].fact.Key < facts[right].fact.Key
		}
		return facts[left].index < facts[right].index
	})
	if tokenBudget < 0 {
		tokenBudget = 0
	}
	lines := make([]string, 0, len(facts))
	used := 0
	for _, item := range facts {
		line := "- " + item.fact.Key + "：" + item.fact.Value
		cost := estimateTextTokens(line)
		if used+cost > tokenBudget {
			break
		}
		lines = append(lines, line)
		used += cost
	}
	truncated := len(lines) < len(facts)
	result := BlackboardRender{Shown: len(lines), Omitted: len(facts) - len(lines), Truncated: truncated}
	result.Text = fmt.Sprintf(`<project-facts shown="%d" omitted="%d" truncated="%t">`, result.Shown, result.Omitted, result.Truncated)
	if len(lines) != 0 {
		result.Text += "\n" + strings.Join(lines, "\n") + "\n"
	}
	result.Text += "</project-facts>"
	return result
}

func providerSystemMessage(request ContextRequest) string {
	return request.SystemPrompt + request.FactIndex
}

func normalizedEnvelope(request ContextRequest) string {
	var builder strings.Builder
	builder.WriteString(providerSystemMessage(request))
	for _, tool := range request.Tools {
		if tool == nil {
			continue
		}
		builder.WriteString(tool.Name())
		builder.WriteByte('\n')
		builder.WriteString(tool.Description())
		if provider, ok := tool.(agent.ToolSchemaProvider); ok {
			encoded, _ := json.Marshal(provider.InputSchema())
			builder.Write(encoded)
		}
	}
	for _, node := range request.Nodes {
		builder.WriteString(node.ID)
		builder.WriteByte(':')
		builder.WriteString(string(node.Kind))
		builder.WriteByte(':')
		builder.WriteString(node.Content)
		if node.Kind == agent.SurfaceNodeSource {
			builder.WriteString(renderMeasuredMessage(request.Messages[node.SourceStartSeq]))
		}
	}
	return builder.String()
}
