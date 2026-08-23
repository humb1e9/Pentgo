package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pentgo/internal/adapters/llm"
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

func TestAssemblerUsesLegacyFullTranscriptWhenContextDisabledWithNilMeter(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{{Role: agent.RoleUser, Content: "legacy"}})
	assembler := NewContextAssembler(fixture.runtime, config.AgentContextConfig{}, nil, nil)
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil)
	if err != nil || len(activities) != 0 || len(input.Messages) != 1 || input.Messages[0].Content != "legacy" {
		t.Fatalf("input/activities/error = %#v/%#v/%v", input, activities, err)
	}
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
	if len(activities) != 0 || len(input.Messages) != 2 || input.Messages[0].Content != "first" || input.Messages[1].Content != "second" || input.ContextWindow != 0 || input.ProjectFacts != "" {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerInjectsBoundedStructuredFactIndex(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	if err := fixture.runtime.ProjectFacts().Upsert(context.Background(), storage.ProjectFactWrite{Fact: domain.ProjectFact{
		FactKey: "target", Category: domain.FactCategoryTarget, Summary: "API target", Body: "secret full reproducible body", Confidence: domain.FactConfidenceTentative, Pinned: true,
	}}); err != nil {
		t.Fatal(err)
	}
	policy := config.AgentContextConfig{ContextWindow: 1000, FactIndexRatio: 0.5}.Effective()
	input, activities, err := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil).Prepare(context.Background(), fixture.session.ID, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 || !strings.Contains(input.ProjectFacts, `<project-fact-index shown="1"`) || !strings.Contains(input.ProjectFacts, "API target") || strings.Contains(input.ProjectFacts, "secret full reproducible body") {
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

func TestContextRequestMeasuresExactProviderSystemEnvelope(t *testing.T) {
	input := agent.ModelStepInput{
		SystemPrompt:    "extra instruction",
		ProjectFacts:    "<project-facts>fact</project-facts>",
		SurfaceNodes:    []agent.SurfaceNode{{Kind: agent.SurfaceNodeSource, SourceStartSeq: 1, SourceEndSeq: 1}},
		SurfaceMessages: map[int]agent.Message{1: {Role: agent.RoleUser, Content: "request"}},
	}
	request := contextRequestFromInput(input)
	wantSystem := llm.SystemInstructionPrefix(input.SystemPrompt)
	wantFacts := llm.ProjectFactsEnvelope(input.ProjectFacts)
	if request.SystemPrompt != wantSystem || request.FactIndex != wantFacts {
		t.Fatalf("context envelope = %#v, want system=%q facts=%q", request, wantSystem, wantFacts)
	}
	if got := llm.SystemPrompt(input.SystemPrompt, input.ProjectFacts); got != request.SystemPrompt+request.FactIndex {
		t.Fatalf("provider prompt = %q, measured prompt = %q", got, request.SystemPrompt+request.FactIndex)
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

func TestOverflowRecoveryCommitsPruneWithoutCheckpointWhenItNowFits(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{
		{Role: agent.RoleUser, Content: "inspect"},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 500)},
	})
	policy := config.AgentContextConfig{
		ContextWindow:            1000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}.Effective()
	summarizer := &checkpointSummarizerFixture{output: agent.CheckpointOutput{Text: "must not run"}}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), summarizer)
	input, activities, err := assembler.PrepareOverflowRecovery(context.Background(), fixture.session.ID, "system", nil)
	if err != nil {
		t.Fatal(err)
	}
	if summarizer.calls != 0 || !strings.Contains(input.Messages[2].Content, toolResultMiddleMarker) {
		t.Fatalf("summarizer/input = %d/%#v", summarizer.calls, input)
	}
	if len(activities) != 2 || activities[0].Kind != agent.ContextToolPruned || activities[1].Kind != agent.ContextOverflowRetry {
		t.Fatalf("activities = %#v", activities)
	}
	snapshot, err := fixture.runtime.ContextSurface(fixture.session.ID).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Nodes[2].Kind != agent.SurfaceNodePrunedTool {
		t.Fatalf("surface = %#v", snapshot)
	}
}

func TestOverflowRecoveryLeavesSurfaceUnchangedWhenCheckpointFails(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []agent.Message{
		{Role: agent.RoleUser, Content: strings.Repeat("old context ", 500)},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 500)},
		{Role: agent.RoleAssistant, Content: "recent"},
	})
	policy := config.AgentContextConfig{
		ContextWindow:            1000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}.Effective()
	surfaceStore := fixture.runtime.ContextSurface(fixture.session.ID)
	before, err := surfaceStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), &checkpointSummarizerFixture{err: errors.New("summary failed")})
	if _, _, err := assembler.PrepareOverflowRecovery(context.Background(), fixture.session.ID, "system", nil); err == nil {
		t.Fatal("overflow recovery succeeded despite checkpoint failure")
	}
	after, err := surfaceStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !sameSurface(before, after) {
		t.Fatalf("surface changed after failed recovery checkpoint: before=%#v after=%#v", before, after)
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
