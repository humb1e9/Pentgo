package app

import (
	"context"
	"strings"
	"testing"

	"pentgo/internal/agent"
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

func TestContextMeterIncludesSystemToolsFactIndexAndSurface(t *testing.T) {
	meter := NewContextMeter()
	measurement := meter.Measure(ContextRequest{
		SystemPrompt: "system",
		Tools:        []agent.Tool{&contextMeterFixtureTool{}},
		FactIndex:    "<project-fact-index>facts</project-fact-index>",
		Nodes:        []agent.SurfaceNode{{Kind: agent.SurfaceNodeCheckpoint, Content: "checkpoint"}},
		Messages:     map[int]agent.Message{1: {Role: agent.RoleUser, Content: "source"}},
	})
	if measurement.SystemTokens == 0 || measurement.ToolSchemaTokens == 0 || measurement.FactIndexTokens == 0 || measurement.SurfaceTokens == 0 {
		t.Fatalf("measurement = %#v", measurement)
	}
	if measurement.TotalTokens != measurement.SystemTokens+measurement.ToolSchemaTokens+measurement.FactIndexTokens+measurement.SurfaceTokens {
		t.Fatalf("measurement = %#v", measurement)
	}
}

func TestContextMeterRoundsFinalFactIndexEnvelopeOnce(t *testing.T) {
	request := ContextRequest{SystemPrompt: "abc", FactIndex: "d"}
	measurement := NewContextMeter().Measure(request)
	if measurement.TotalTokens != 1 || measurement.SystemTokens+measurement.FactIndexTokens != 1 {
		t.Fatalf("measurement = %#v", measurement)
	}
	if normalizedEnvelope(request)[:4] != "abcd" {
		t.Fatalf("normalized envelope = %q", normalizedEnvelope(request))
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
