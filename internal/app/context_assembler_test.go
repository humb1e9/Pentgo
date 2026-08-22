package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/domain"
)

type countingContextMeter struct {
	inner ContextMeter
	calls int
}

func (meter *countingContextMeter) Measure(request ContextRequest) agent.ContextMeasurement {
	meter.calls++
	return meter.inner.Measure(request)
}

func TestAssemblerUsesLegacyFullTranscriptWhenContextDisabled(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{
		{Role: agent.RoleUser, Content: "first"},
		{Role: agent.RoleAssistant, Content: "second"},
	})
	assembler := NewContextAssembler(fixture.runtime, config.AgentContextConfig{}, NewContextMeter(), nil)
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 || len(input.Messages) != 2 || input.Messages[0].Content != "first" || input.Messages[1].Content != "second" || input.ContextWindow != 0 {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerMeasuresBeforeEveryPrepareCall(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{{Role: agent.RoleUser, Content: "small"}})
	meter := &countingContextMeter{inner: NewContextMeter()}
	policy := config.AgentContextConfig{ContextWindow: 1000}.Effective()
	assembler := NewContextAssembler(fixture.runtime, policy, meter, nil)
	for round := 0; round < 2; round++ {
		if _, _, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil); err != nil {
			t.Fatal(err)
		}
	}
	if meter.calls != 2 {
		t.Fatalf("measure calls = %d, want 2", meter.calls)
	}
}

func TestAssemblerPrunesBeforeCheckpoint(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{
		{Role: agent.RoleUser, Content: "inspect"},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 400)},
		{Role: agent.RoleAssistant, Content: "recent"},
	})
	policy := config.AgentContextConfig{
		ContextWindow:            1000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}.Effective()
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Kind != agent.ContextToolPruned || !strings.Contains(input.Messages[2].Content, "[... tool result middle pruned ...]") {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerRejectsRequestWhenFixedEnvelopeExceedsThreshold(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{{Role: agent.RoleUser, Content: "raw"}})
	policy := config.AgentContextConfig{ContextWindow: 100}.Effective()
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	_, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, strings.Repeat("system ", 100), nil)
	if !errors.Is(err, ErrContextPreflight) || len(activities) != 1 || activities[0].Kind != agent.ContextRequestRejected {
		t.Fatalf("error/activities = %v/%#v", err, activities)
	}
}

func TestAssemblerReturnsSurfaceCheckpointThenRawTailInOrder(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{
		{Role: agent.RoleUser, Content: "old user"},
		{Role: agent.RoleAssistant, Content: "old answer"},
		{Role: agent.RoleUser, Content: "recent user"},
	})
	surface := fixture.runtime.ContextSurface(fixture.session.ID)
	if _, err := surface.StartCompaction(0, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 1, 2, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 2, Content: "checkpoint"}); err != nil {
		t.Fatal(err)
	}
	policy := config.AgentContextConfig{ContextWindow: 1000}.Effective()
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	input, _, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Messages) != 2 || input.Messages[0].Role != agent.RoleUser || input.Messages[0].Content != "checkpoint" || input.Messages[1].Content != "recent user" {
		t.Fatalf("messages = %#v", input.Messages)
	}
}

type assemblerFixture struct {
	store   *storage.ProjectStore
	runtime *ProjectRuntime
	session *domain.Session
}

func newAssemblerFixture(t *testing.T) assemblerFixture {
	t.Helper()
	store, err := storage.CreateProjectStore(t.TempDir(), "assembler", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenProjectRuntime(context.Background(), store, nil)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := runtime.SetTurnHandler(func(context.Context, *domain.Session, string) error { return nil }); err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	session, err := runtime.NewSession("assemble")
	if err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	return assemblerFixture{store: store, runtime: runtime, session: session}
}

func (fixture assemblerFixture) close() { _ = fixture.runtime.Close() }

func appendAssemblerMessages(t *testing.T, fixture assemblerFixture, messages []agent.Message) {
	t.Helper()
	transcript := fixture.runtime.Transcript(fixture.session.ID)
	for _, message := range messages {
		if err := transcript.Append(message); err != nil {
			t.Fatal(err)
		}
	}
}
