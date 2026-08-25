package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"pentgo/internal/core"
	llm "pentgo/internal/model"
	"pentgo/internal/project"
	sessionstate "pentgo/internal/project/session"
)

const toolResultMiddleMarker = "\n\n[... tool result middle pruned ...]\n\n"

type checkpointSummarizerFixture struct {
	output core.CheckpointOutput
	err    error
	calls  int
}

func (fixture *checkpointSummarizerFixture) Summarize(_ context.Context, _ core.CheckpointInput) (core.CheckpointOutput, error) {
	fixture.calls++
	return fixture.output, fixture.err
}

func sameSurface(left, right core.ContextSurface) bool { return reflect.DeepEqual(left, right) }

type countingContextMeter struct {
	inner ContextMeter
	calls int
}

func (meter *countingContextMeter) Measure(request ContextRequest) core.ContextMeasurement {
	meter.calls++
	return meter.inner.Measure(request)
}

func TestAssemblerUsesFullConversationAndInjectsSuppliedFactsWhenContextDisabled(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{{Role: core.RoleUser, Content: "legacy"}})
	assembler := NewContextAssembler(fixture.runtime, project.ContextConfig{}, nil, nil)
	facts := `<project-facts shown="1" omitted="0">\n- api_base_url: https://target\n</project-facts>`
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil, facts)
	if err != nil || len(activities) != 0 || len(input.Messages) != 1 || input.Messages[0].Content != "legacy" || input.ProjectFacts != facts {
		t.Fatalf("input/activities/error = %#v/%#v/%v", input, activities, err)
	}
}

func TestAssemblerUsesLegacyFullConversationWhenContextDisabled(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleAssistant, Content: "second"},
	})
	assembler := NewContextAssembler(fixture.runtime, project.ContextConfig{}, NewContextMeter(), nil)
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 || len(input.Messages) != 2 || input.Messages[0].Content != "first" || input.Messages[1].Content != "second" || input.ContextWindow != 0 || input.ProjectFacts != "" {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerUsesSuppliedFactSnapshotWithoutReadingLedger(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	policy := project.ContextConfig{ContextWindow: 2000}
	snapshot := `<project-facts shown="1" omitted="0">\n- api_base_url: https://target\n</project-facts>`
	input, activities, err := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil).Prepare(context.Background(), fixture.session.ID, "system", nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 || input.ProjectFacts != snapshot {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerMeasuresBeforeEveryPrepareCall(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{{Role: core.RoleUser, Content: "small"}})
	meter := &countingContextMeter{inner: NewContextMeter()}
	policy := project.ContextConfig{ContextWindow: 2000}
	assembler := NewContextAssembler(fixture.runtime, policy, meter, nil)
	for round := 0; round < 2; round++ {
		if _, _, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if meter.calls != 2 {
		t.Fatalf("measure calls = %d, want 2", meter.calls)
	}
}

func TestContextRequestMeasuresExactProviderSystemEnvelope(t *testing.T) {
	input := core.ModelStepInput{
		SystemPrompt:    "extra instruction",
		ProjectFacts:    "<project-facts>fact</project-facts>",
		SurfaceNodes:    []core.SurfaceNode{{Kind: core.SurfaceNodeSource, SourceStartSeq: 1, SourceEndSeq: 1}},
		SurfaceMessages: map[int]core.Message{1: {Role: core.RoleUser, Content: "request"}},
	}
	request := contextRequestFromInput(input)
	wantSystem := llm.SystemInstructionPrefix(input.SystemPrompt)
	wantFacts := llm.ProjectFactsEnvelope(input.ProjectFacts)
	if request.SystemPrompt != wantSystem || request.ProjectFacts != wantFacts {
		t.Fatalf("context envelope = %#v, want system=%q facts=%q", request, wantSystem, wantFacts)
	}
	if got := llm.SystemPrompt(input.SystemPrompt, input.ProjectFacts); got != request.SystemPrompt+request.ProjectFacts {
		t.Fatalf("provider prompt = %q, measured prompt = %q", got, request.SystemPrompt+request.ProjectFacts)
	}
}

func TestAssemblerPrunesBeforeCheckpoint(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: "inspect"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: core.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 2000)},
		{Role: core.RoleAssistant, Content: "recent"},
	})
	policy := project.ContextConfig{
		ContextWindow:            4000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	input, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 || activities[0].Kind != core.ContextToolPruned || !strings.Contains(input.Messages[2].Content, "[... tool result middle pruned ...]") {
		t.Fatalf("input/activities = %#v/%#v", input, activities)
	}
}

func TestAssemblerRejectsRequestWhenFixedEnvelopeExceedsThreshold(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{{Role: core.RoleUser, Content: "raw"}})
	policy := project.ContextConfig{ContextWindow: 100}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	_, activities, err := assembler.Prepare(context.Background(), fixture.session.ID, strings.Repeat("system ", 100), nil, "")
	if !errors.Is(err, ErrContextPreflight) || len(activities) != 1 || activities[0].Kind != core.ContextRequestRejected {
		t.Fatalf("error/activities = %v/%#v", err, activities)
	}
}

func TestOverflowRecoveryCommitsPruneWithoutCheckpointWhenItNowFits(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: "inspect"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: core.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 500)},
	})
	policy := project.ContextConfig{
		ContextWindow:            4000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}
	summarizer := &checkpointSummarizerFixture{output: core.CheckpointOutput{Text: "must not run"}}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), summarizer)
	input, activities, err := assembler.PrepareOverflowRecovery(context.Background(), fixture.session.ID, "system", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if summarizer.calls != 0 || !strings.Contains(input.Messages[2].Content, toolResultMiddleMarker) {
		t.Fatalf("summarizer/input = %d/%#v", summarizer.calls, input)
	}
	if len(activities) != 2 || activities[0].Kind != core.ContextToolPruned || activities[1].Kind != core.ContextOverflowRetry {
		t.Fatalf("activities = %#v", activities)
	}
	snapshot, err := fixture.runtime.ContextSurface(fixture.session.ID).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Nodes[2].Kind != core.SurfaceNodePrunedTool {
		t.Fatalf("surface = %#v", snapshot)
	}
}

func TestOverflowRecoveryCheckpointsEvenWhenLocalEstimateFits(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: strings.Repeat("old ", 500)},
		{Role: core.RoleAssistant, Content: strings.Repeat("answer ", 500)},
		{Role: core.RoleUser, Content: strings.Repeat("recent ", 500)},
	})
	policy := project.ContextConfig{ContextWindow: 4000}
	summarizer := &checkpointSummarizerFixture{output: core.CheckpointOutput{Text: "checkpoint"}}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), summarizer)
	input, activities, err := assembler.PrepareOverflowRecovery(context.Background(), fixture.session.ID, "system", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if summarizer.calls != 1 || len(input.Messages) >= 3 {
		t.Fatalf("summarizer calls/messages = %d/%#v", summarizer.calls, input.Messages)
	}
	if len(activities) == 0 || activities[len(activities)-1].Kind != core.ContextOverflowRetry {
		t.Fatalf("activities = %#v", activities)
	}
}

func TestOverflowRecoveryLeavesSurfaceUnchangedWhenCheckpointFails(t *testing.T) {
	fixture := newAssemblerFixture(t)
	defer fixture.close()
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: strings.Repeat("old context ", 500)},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call-1", Name: "probe"}}},
		{Role: core.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: strings.Repeat("tool-output ", 500)},
		{Role: core.RoleAssistant, Content: "recent"},
	})
	policy := project.ContextConfig{
		ContextWindow:            1000,
		ToolResultThresholdChars: 100,
		ToolResultHeadChars:      20,
		ToolResultTailChars:      20,
	}
	surfaceStore := fixture.runtime.ContextSurface(fixture.session.ID)
	before, err := surfaceStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), &checkpointSummarizerFixture{err: errors.New("summary failed")})
	if _, _, err := assembler.PrepareOverflowRecovery(context.Background(), fixture.session.ID, "system", nil, ""); err == nil {
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
	appendAssemblerMessages(t, fixture, []core.Message{
		{Role: core.RoleUser, Content: "old user"},
		{Role: core.RoleAssistant, Content: "old answer"},
		{Role: core.RoleUser, Content: "recent user"},
	})
	surface := fixture.runtime.ContextSurface(fixture.session.ID)
	if _, err := surface.StartCompaction(0, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 1, 2, core.SurfaceNode{Kind: core.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 2, Content: "checkpoint"}); err != nil {
		t.Fatal(err)
	}
	policy := project.ContextConfig{ContextWindow: 2000}
	assembler := NewContextAssembler(fixture.runtime, policy, NewContextMeter(), nil)
	input, _, err := assembler.Prepare(context.Background(), fixture.session.ID, "system", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Messages) != 2 || input.Messages[0].Role != core.RoleUser || input.Messages[0].Content != "checkpoint" || input.Messages[1].Content != "recent user" {
		t.Fatalf("messages = %#v", input.Messages)
	}
}

type assemblerFixture struct {
	store   *project.ProjectStore
	runtime *ProjectRuntime
	session *sessionstate.Session
}

func newAssemblerFixture(t *testing.T) assemblerFixture {
	t.Helper()
	store, err := project.CreateProjectStore(t.TempDir(), "assembler", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenProjectRuntime(context.Background(), store, nil)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := runtime.SetTurnHandler(func(context.Context, *sessionstate.Session, string) error { return nil }); err != nil {
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

func appendAssemblerMessages(t *testing.T, fixture assemblerFixture, messages []core.Message) {
	t.Helper()
	conversation := fixture.runtime.Conversation(fixture.session.ID)
	for _, message := range messages {
		if err := conversation.Append(message); err != nil {
			t.Fatal(err)
		}
	}
}
