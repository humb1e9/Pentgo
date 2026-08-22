package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

// scriptedStepper emits deterministic events for host-controlled model steps
// without depending on a model provider.
type scriptedStepper struct {
	stream func(context.Context, agent.ModelStepInput) []agent.ModelStreamEvent
}

func (stepper scriptedStepper) StreamStep(ctx context.Context, input agent.ModelStepInput) (<-chan agent.ModelStreamEvent, error) {
	events := make(chan agent.ModelStreamEvent, 16)
	go func() {
		defer close(events)
		for _, event := range stepper.stream(ctx, input) {
			events <- event
		}
	}()
	return events, nil
}

// fixtureTool represents an external tool whose application wrapper must record
// evidence when the host loop invokes it.
type fixtureTool struct {
	onInvoke func()
}

func (*fixtureTool) Name() string        { return "fixture_probe" }
func (*fixtureTool) Description() string { return "执行一个本地 fixture 检查。" }
func (*fixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
}
func (tool *fixtureTool) Invoke(context.Context, map[string]any) (string, error) {
	if tool.onInvoke != nil {
		tool.onInvoke()
	}
	return "fixture result", nil
}

type orderedFixtureTool struct {
	name    string
	waitFor <-chan struct{}
	done    chan<- string
}

func (tool *orderedFixtureTool) Name() string        { return tool.name }
func (tool *orderedFixtureTool) Description() string { return "测试并发工具排序。" }
func (tool *orderedFixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (tool *orderedFixtureTool) Invoke(context.Context, map[string]any) (string, error) {
	if tool.waitFor != nil {
		<-tool.waitFor
	}
	if tool.done != nil {
		tool.done <- tool.name
	}
	return tool.name + " result", nil
}

// newApplicationFixture assembles the same runtime, transcript, and tool
// boundaries as production while replacing only the model stepper.
func newApplicationFixture(t *testing.T, stepper agent.ModelStepper, external ...agent.Tool) (*ProjectRuntime, *domain.Session, *TurnService) {
	t.Helper()
	store, err := storage.CreateProjectStore(t.TempDir(), "fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	projectRuntime, err := OpenProjectRuntime(context.Background(), store, staticTools(external))
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(stepper, store, nil)
	if err := projectRuntime.SetTurnHandler(func(ctx context.Context, session *domain.Session, message string) error {
		return service.RunTurn(ctx, projectRuntime, session, message)
	}); err != nil {
		t.Fatal(err)
	}
	session, err := projectRuntime.NewSession("inspect fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projectRuntime.Close() })
	return projectRuntime, session, service
}

type staticTools []agent.Tool

func (tools staticTools) Tools(context.Context) ([]agent.Tool, error) { return tools, nil }

type countingContextPreparer struct {
	runtime         *ProjectRuntime
	prepareCalls    int
	overflowRetries int
}

func (preparer *countingContextPreparer) Prepare(_ context.Context, sessionID, systemPrompt string, tools []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error) {
	preparer.prepareCalls++
	return agent.ModelStepInput{SessionID: sessionID, Messages: preparer.runtime.Transcript(sessionID).Messages(), SystemPrompt: systemPrompt, Tools: append([]agent.Tool(nil), tools...)}, nil, nil
}

func (preparer *countingContextPreparer) PrepareOverflowRecovery(_ context.Context, sessionID, systemPrompt string, tools []agent.Tool) (agent.ModelStepInput, []agent.ContextActivity, error) {
	preparer.overflowRetries++
	return agent.ModelStepInput{SessionID: sessionID, Messages: preparer.runtime.Transcript(sessionID).Messages(), SystemPrompt: systemPrompt, Tools: append([]agent.Tool(nil), tools...)}, []agent.ContextActivity{{Kind: agent.ContextOverflowRetry, Message: "recovered"}}, nil
}

func TestTurnPersistsConcurrentToolResultsInProviderCallOrder(t *testing.T) {
	secondDone := make(chan string, 1)
	releaseFirst := make(chan struct{})
	go func() {
		<-secondDone
		close(releaseFirst)
	}()
	steps := 0
	stepper := scriptedStepper{stream: func(_ context.Context, input agent.ModelStepInput) []agent.ModelStreamEvent {
		steps++
		switch steps {
		case 1:
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{
				{ID: "call-a", Name: "first"},
				{ID: "call-b", Name: "second"},
			}}}}
		case 2:
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, Content: "complete"}}}
		default:
			return []agent.ModelStreamEvent{{Err: errors.New("unexpected step")}}
		}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, stepper,
		&orderedFixtureTool{name: "first", waitFor: releaseFirst},
		&orderedFixtureTool{name: "second", done: secondDone},
	)
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "run concurrent tools"); err != nil {
		t.Fatal(err)
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 5 || len(messages[1].ToolCalls) != 2 || messages[2].ToolName != "first" || messages[3].ToolName != "second" || messages[4].Content != "complete" {
		t.Fatalf("transcript order = %#v", messages)
	}
}

func TestTurnPersistsUserToolAssistantMessages(t *testing.T) {
	var mu sync.Mutex
	var inputs []agent.ModelStepInput
	stepper := scriptedStepper{stream: func(_ context.Context, input agent.ModelStepInput) []agent.ModelStreamEvent {
		mu.Lock()
		inputs = append(inputs, input)
		step := len(inputs)
		mu.Unlock()

		switch step {
		case 1:
			if len(input.Messages) != 1 || input.Messages[0].Role != agent.RoleUser {
				return []agent.ModelStreamEvent{{Err: context.Canceled}}
			}
			return []agent.ModelStreamEvent{{
				Delta: agent.Message{Role: agent.RoleAssistant, Content: "检查中"},
				Final: &agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fixture_probe", Arguments: map[string]any{"value": "TARGET"}}}},
			}}
		case 2:
			if len(input.Messages) != 3 || input.Messages[1].Role != agent.RoleAssistant || input.Messages[2].Role != agent.RoleTool || !strings.Contains(input.Messages[2].Content, "fixture result") {
				return []agent.ModelStreamEvent{{Err: context.Canceled}}
			}
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, Content: "已完成检查"}}}
		default:
			return []agent.ModelStreamEvent{{Err: context.Canceled}}
		}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, stepper, &fixtureTool{})
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "检查 TARGET"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("model steps = %d, want 2", len(inputs))
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 4 || messages[0].Role != agent.RoleUser || len(messages[1].ToolCalls) != 1 || messages[2].Role != agent.RoleTool || messages[3].Content != "已完成检查" {
		t.Fatalf("transcript = %#v", messages)
	}
	reopened, err := storage.OpenProjectStore(projectRuntime.Store().Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	transcript, err := reopened.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer transcript.Close()
	if len(transcript.Messages()) != 4 {
		t.Fatalf("durable transcript = %#v", transcript.Messages())
	}
	if record, ok := projectRuntime.Evidence().Lookup(1); !ok || !record.Success || !strings.Contains(record.Output, "evidence_ref: 1") {
		t.Fatalf("evidence = %#v, %t", record, ok)
	}
}

func TestOverflowRecoveryRetriesFailedRequestWithoutRepeatingTool(t *testing.T) {
	var stepCalls, toolCalls int
	stepper := scriptedStepper{stream: func(_ context.Context, input agent.ModelStepInput) []agent.ModelStreamEvent {
		stepCalls++
		switch stepCalls {
		case 1:
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fixture_probe", Arguments: map[string]any{"value": "once"}}}}}}
		case 2:
			return []agent.ModelStreamEvent{{Err: agent.ErrContextWindowExceeded}}
		case 3:
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, Content: "recovered"}}}
		default:
			return []agent.ModelStreamEvent{{Err: errors.New("unexpected model request")}}
		}
	}}
	projectRuntime, session, service := newApplicationFixture(t, stepper, &fixtureTool{onInvoke: func() { toolCalls++ }})
	preparer := &countingContextPreparer{runtime: projectRuntime}
	service.SetContextAssembler(preparer)
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "recover once"); err != nil {
		t.Fatal(err)
	}
	if stepCalls != 3 || toolCalls != 1 || preparer.prepareCalls != 2 || preparer.overflowRetries != 1 {
		t.Fatalf("steps/tools/prepares/retries = %d/%d/%d/%d", stepCalls, toolCalls, preparer.prepareCalls, preparer.overflowRetries)
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 4 || messages[1].Role != agent.RoleAssistant || messages[2].Role != agent.RoleTool || messages[3].Content != "recovered" {
		t.Fatalf("transcript = %#v", messages)
	}
}

func TestTurnRejectsMalformedRawToolArgumentsBeforePersistenceOrInvocation(t *testing.T) {
	var invocations int
	stepper := scriptedStepper{stream: func(context.Context, agent.ModelStepInput) []agent.ModelStreamEvent {
		return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fixture_probe", RawArguments: `{"value":`}}}}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, stepper, &fixtureTool{onInvoke: func() { invocations++ }})
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "检查 malformed arguments"); err == nil {
		t.Fatal("malformed tool arguments returned nil")
	}
	if invocations != 0 {
		t.Fatalf("tool invocations = %d, want 0", invocations)
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 1 || messages[0].Role != agent.RoleUser {
		t.Fatalf("transcript = %#v, want only user message", messages)
	}
}

func TestConsumeModelStreamRejectsOutputAfterFinal(t *testing.T) {
	stream := make(chan agent.ModelStreamEvent, 2)
	stream <- agent.ModelStreamEvent{Final: &agent.Message{Role: agent.RoleAssistant, Content: "complete"}}
	stream <- agent.ModelStreamEvent{Delta: agent.Message{Role: agent.RoleAssistant, Content: "late"}}
	close(stream)
	if _, err := consumeModelStream(context.Background(), nil, "session", "turn", stream); err == nil || !strings.Contains(err.Error(), "after final") {
		t.Fatalf("post-final output error = %v", err)
	}
}

func TestConsumeModelStreamRejectsErrorAfterFinal(t *testing.T) {
	stream := make(chan agent.ModelStreamEvent, 2)
	stream <- agent.ModelStreamEvent{Final: &agent.Message{Role: agent.RoleAssistant, Content: "complete"}}
	stream <- agent.ModelStreamEvent{Err: errors.New("late stream failure")}
	close(stream)
	if _, err := consumeModelStream(context.Background(), nil, "session", "turn", stream); err == nil || !strings.Contains(err.Error(), "after final") {
		t.Fatalf("post-final error = %v", err)
	}
}

func TestTurnDoneLeavesSessionReusable(t *testing.T) {
	stepper := scriptedStepper{stream: func(context.Context, agent.ModelStepInput) []agent.ModelStreamEvent {
		return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, Content: "继续对话"}}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, stepper)
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "第一轮"); err != nil {
		t.Fatal(err)
	}
	snapshot := projectRuntime.Snapshot(session.ID)
	if snapshot == nil || snapshot.Turns != 1 || snapshot.ActiveTurnID != "" {
		t.Fatalf("session = %#v", snapshot)
	}
}

func TestRuntimeToolsExposeAutomaticSkillLoaderWhenCatalogExists(t *testing.T) {
	projectRuntime, session, _ := newApplicationFixture(t, scriptedStepper{})
	loader := func(string) (string, error) { return "body", nil }
	tools, err := newRuntimeToolProvider(projectRuntime, session, nil, loader, true).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute", "write_project_fact", "load_skill"}
	if len(tools) != len(want) {
		t.Fatalf("tools = %#v", tools)
	}
	for index, name := range want {
		if tools[index].Name() != name {
			t.Fatalf("tool %d = %q, want %q", index, tools[index].Name(), name)
		}
	}
}

func TestRuntimeToolsExposeOnlyWriteProjectFact(t *testing.T) {
	projectRuntime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(projectRuntime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute", "write_project_fact"}
	if len(tools) != len(want) {
		t.Fatalf("tools = %#v", tools)
	}
	for index, name := range want {
		if tools[index].Name() != name {
			t.Fatalf("tool %d = %q, want %q", index, tools[index].Name(), name)
		}
	}
	if _, err := tools[7].Invoke(context.Background(), map[string]any{"key": "base_url", "value": "https://TARGET"}); err != nil {
		t.Fatal(err)
	}
	board := projectRuntime.Blackboard()
	if len(board.Facts) != 1 || board.Facts[0].Key != "base_url" || board.Facts[0].Value != "https://TARGET" {
		t.Fatalf("blackboard = %#v", board)
	}
}

func TestCancelledTurnLeavesDurableEvidence(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	stepper := scriptedStepper{stream: func(ctx context.Context, input agent.ModelStepInput) []agent.ModelStreamEvent {
		if len(input.Messages) == 1 {
			return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fixture_probe", Arguments: map[string]any{"value": "before-cancel"}}}}}}
		}
		<-release
		return []agent.ModelStreamEvent{{Final: &agent.Message{Role: agent.RoleAssistant, Content: "不应成为完成消息"}}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, stepper, &fixtureTool{onInvoke: func() { once.Do(func() { close(started) }) }})
	// Cancel after the host has invoked the tool, so persisted evidence survives
	// the subsequent interrupted model step.
	requestContext, cancel := context.WithCancel(context.Background())
	done := projectRuntime.Submit(requestContext, session.ID, "开始检查")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool did not run")
	}
	cancel()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("cancelled turn returned nil")
	}
	record, ok := projectRuntime.Evidence().Lookup(1)
	if !ok || !record.Success {
		t.Fatalf("durable evidence = %#v, %t", record, ok)
	}
	if snapshot := projectRuntime.Snapshot(session.ID); snapshot == nil || snapshot.ActiveTurn == nil || snapshot.ActiveTurn.Status != domain.TurnInterrupted {
		t.Fatalf("cancelled session = %#v", snapshot)
	}
	if _, err := filepath.Abs(projectRuntime.Store().Root()); err != nil {
		t.Fatal(err)
	}
}
