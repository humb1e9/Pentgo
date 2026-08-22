package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

// scriptedEngine 在不依赖模型 Provider 的情况下输出确定性事件序列。
type scriptedEngine struct {
	run func(context.Context, agent.TurnInput) []agent.TurnEvent
}

func (engine scriptedEngine) Run(ctx context.Context, input agent.TurnInput) (<-chan agent.TurnEvent, error) {
	events := make(chan agent.TurnEvent, 16)
	go func() {
		defer close(events)
		for _, event := range engine.run(ctx, input) {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// fixtureTool 表示一个外部工具，其应用层包装器必须在脚本化模型接收输出前记录证据。
type fixtureTool struct{}

func (*fixtureTool) Name() string        { return "fixture_probe" }
func (*fixtureTool) Description() string { return "执行一个本地 fixture 检查。" }
func (*fixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
}
func (*fixtureTool) Invoke(context.Context, map[string]any) (string, error) {
	return "fixture result", nil
}

// newApplicationFixture 装配与生产环境相同的运行时、transcript 和工具边界，
// 仅将模型替换为脚本化引擎。
func newApplicationFixture(t *testing.T, engine agent.ModelEngine, external ...agent.Tool) (*ProjectRuntime, *domain.Session, *TurnService) {
	t.Helper()
	store, err := storage.CreateProjectStore(t.TempDir(), "fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	projectRuntime, err := OpenProjectRuntime(context.Background(), store, staticTools(external))
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(engine, store, nil)
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

func TestTurnPersistsUserToolAssistantMessages(t *testing.T) {
	engine := scriptedEngine{run: func(_ context.Context, input agent.TurnInput) []agent.TurnEvent {
		if len(input.Messages) != 1 || input.Messages[0].Role != agent.RoleUser {
			return []agent.TurnEvent{{Kind: agent.TurnEventError, Err: fmt.Errorf("user message was not persisted first")}}
		}
		var output string
		for _, tool := range input.Tools {
			if tool.Name() == "fixture_probe" {
				output, _ = tool.Invoke(context.Background(), map[string]any{"value": "TARGET"})
			}
		}
		return []agent.TurnEvent{
			{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fixture_probe", RawArguments: `{"value":"TARGET"}`}}}},
			{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "fixture_probe", Content: output}},
			{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleAssistant, Content: "已完成检查"}},
		}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, engine, &fixtureTool{})
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "检查 TARGET"); err != nil {
		t.Fatal(err)
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

func TestTurnDoneLeavesSessionReusable(t *testing.T) {
	engine := scriptedEngine{run: func(context.Context, agent.TurnInput) []agent.TurnEvent {
		return []agent.TurnEvent{{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleAssistant, Content: "继续对话"}}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, engine)
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "第一轮"); err != nil {
		t.Fatal(err)
	}
	snapshot := projectRuntime.Snapshot(session.ID)
	if snapshot == nil || snapshot.Turns != 1 || snapshot.ActiveTurnID != "" {
		t.Fatalf("session = %#v", snapshot)
	}
}

func TestRuntimeToolsExposeAutomaticSkillLoaderWhenCatalogExists(t *testing.T) {
	projectRuntime, session, _ := newApplicationFixture(t, scriptedEngine{run: func(context.Context, agent.TurnInput) []agent.TurnEvent {
		return nil
	}})
	loader := func(string) (string, error) { return "body", nil }
	tools, err := newRuntimeToolProvider(projectRuntime, session, nil, loader, true).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Name() != "write_project_fact" || tools[1].Name() != "load_skill" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestRuntimeToolsExposeOnlyWriteProjectFact(t *testing.T) {
	projectRuntime, session, _ := newApplicationFixture(t, scriptedEngine{run: func(context.Context, agent.TurnInput) []agent.TurnEvent {
		return nil
	}})
	tools, err := newRuntimeToolProvider(projectRuntime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "write_project_fact" {
		t.Fatalf("tools = %#v", tools)
	}
	if _, err := tools[0].Invoke(context.Background(), map[string]any{"key": "base_url", "value": "https://TARGET"}); err != nil {
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
	engine := scriptedEngine{run: func(ctx context.Context, input agent.TurnInput) []agent.TurnEvent {
		for _, tool := range input.Tools {
			if tool.Name() == "fixture_probe" {
				_, _ = tool.Invoke(context.Background(), map[string]any{"value": "before-cancel"})
			}
		}
		close(started)
		<-release
		return []agent.TurnEvent{{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleAssistant, Content: "不应成为完成消息"}}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, engine, &fixtureTool{})
	// 在工具返回后取消，验证证据能在后续 turn 中止后继续保留。
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
