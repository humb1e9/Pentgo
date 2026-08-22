package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

type contextMeterFixtureTool struct{}

func (*contextMeterFixtureTool) Name() string        { return "fixture" }
func (*contextMeterFixtureTool) Description() string { return "fixture tool" }
func (*contextMeterFixtureTool) Invoke(_ context.Context, _ map[string]any) (string, error) {
	return "", nil
}
func (*contextMeterFixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "string"}}}
}

func TestContextMeterIncludesSystemToolsBlackboardAndSurface(t *testing.T) {
	meter := NewContextMeter()
	measurement := meter.Measure(ContextRequest{
		SystemPrompt: "system",
		Tools:        []agent.Tool{&contextMeterFixtureTool{}},
		Blackboard:   "facts",
		Nodes:        []agent.SurfaceNode{{Kind: agent.SurfaceNodeCheckpoint, Content: "checkpoint"}},
		Messages:     map[int]agent.Message{1: {Role: agent.RoleUser, Content: "source"}},
	})
	if measurement.SystemTokens == 0 || measurement.ToolSchemaTokens == 0 || measurement.BlackboardTokens == 0 || measurement.SurfaceTokens == 0 {
		t.Fatalf("measurement = %#v", measurement)
	}
	if measurement.TotalTokens != measurement.SystemTokens+measurement.ToolSchemaTokens+measurement.BlackboardTokens+measurement.SurfaceTokens {
		t.Fatalf("measurement = %#v", measurement)
	}
}

func TestRenderBoundedBlackboardKeepsMostRecentlyUpdatedFacts(t *testing.T) {
	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	board := &domain.Blackboard{Facts: []domain.Fact{
		{Key: "old", Value: strings.Repeat("old ", 20), At: base, UpdatedAt: base.Add(3 * time.Minute)},
		{Key: "middle", Value: "middle", At: base.Add(time.Minute), UpdatedAt: base.Add(time.Minute)},
		{Key: "new", Value: strings.Repeat("new ", 20), At: base.Add(2 * time.Minute), UpdatedAt: base.Add(2 * time.Minute)},
	}}
	result := RenderBoundedBlackboard(board, 45)
	if !strings.Contains(result.Text, `truncated="true"`) || !strings.Contains(result.Text, `shown="2"`) || !strings.Contains(result.Text, `omitted="1"`) {
		t.Fatalf("rendered = %q", result.Text)
	}
	if !strings.Contains(result.Text, "- old：") || !strings.Contains(result.Text, "- new：") || strings.Contains(result.Text, "- middle：") {
		t.Fatalf("rendered = %q", result.Text)
	}
	if strings.Index(result.Text, "- old：") > strings.Index(result.Text, "- new：") {
		t.Fatalf("facts are not newest first: %q", result.Text)
	}
}

func TestMeasureReturnsIndependentSnapshot(t *testing.T) {
	meter := NewContextMeter()
	nodes := []agent.SurfaceNode{{Kind: agent.SurfaceNodeCheckpoint, Content: "checkpoint"}}
	messages := map[int]agent.Message{1: {Role: agent.RoleUser, Content: "source"}}
	request := ContextRequest{SystemPrompt: "system", Nodes: nodes, Messages: messages}
	measurement := meter.Measure(request)
	nodes[0].Content = strings.Repeat("changed", 100)
	messages[1] = agent.Message{Role: agent.RoleUser, Content: strings.Repeat("changed", 100)}
	if again := meter.Measure(request); again.TotalTokens == measurement.TotalTokens {
		t.Fatal("mutated request did not change new measurement")
	}
	if measurement.SurfaceTokens == 0 || measurement.TotalTokens == 0 {
		t.Fatalf("measurement mutated = %#v", measurement)
	}
}
