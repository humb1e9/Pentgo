package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pentgo/internal/adapters/builtins"
	"pentgo/internal/agent"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedModel struct {
	messages []*schema.Message
	inputs   [][]*schema.Message
}

func (model *scriptedModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	model.inputs = append(model.inputs, append([]*schema.Message(nil), input...))
	if len(model.messages) == 0 {
		return nil, fmt.Errorf("script exhausted")
	}
	message := model.messages[0]
	model.messages = model.messages[1:]
	return message, nil
}

func (*scriptedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported")
}

func (model *scriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return model, nil
}

type engineFixtureTool struct{}

func (*engineFixtureTool) Name() string        { return "fixture_probe" }
func (*engineFixtureTool) Description() string { return "执行 fixture 检查。" }
func (*engineFixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}}
}
func (*engineFixtureTool) Invoke(context.Context, map[string]any) (string, error) {
	return "RESULT", nil
}

func TestEinoEnginePreservesReasoningContentAcrossMessageBoundary(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{{
		Role: schema.Assistant, Content: "ok", ReasoningContent: "reason first",
	}}}
	engine, err := NewEngine(context.Background(), model, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got agent.Message
	for event := range events {
		if event.Message.Role == agent.RoleAssistant {
			got = event.Message
		}
	}
	if got.ReasoningContent != "reason first" {
		t.Fatalf("reasoning content = %q", got.ReasoningContent)
	}

	replayed := toSchemaMessages([]agent.Message{{Role: agent.RoleAssistant, ReasoningContent: "reason replay"}})
	if len(replayed) != 1 || replayed[0].ReasoningContent != "reason replay" {
		t.Fatalf("replayed messages = %#v", replayed)
	}
}

func TestEinoEngineMapsToolCallAndResult(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "fixture_probe", Arguments: `{"value":"TARGET"}`}}}),
		schema.AssistantMessage("已完成", nil),
	}}
	engine, err := NewEngine(context.Background(), model, []agent.Tool{&engineFixtureTool{}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "检查 TARGET"}}})
	if err != nil {
		t.Fatal(err)
	}
	var messages []agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Message.Role != "" {
			messages = append(messages, event.Message)
		}
	}
	if len(messages) < 3 || len(messages[0].ToolCalls) != 1 || messages[1].Role != agent.RoleTool || messages[1].Content != "RESULT" || messages[len(messages)-1].Content != "已完成" {
		t.Fatalf("mapped messages = %#v", messages)
	}
}

func TestEinoEngineUsesChineseSystemPrompt(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{schema.AssistantMessage("收到", nil)}}
	engine, err := NewEngine(context.Background(), model, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{
		{Role: agent.RoleSystem, Content: `<pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">` + "\n- `api`：API routing\n</pentgo-skill-catalog>"},
		{Role: agent.RoleUser, Content: "检查 API"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(model.inputs) != 1 || len(model.inputs[0]) != 3 || model.inputs[0][0].Role != schema.System || model.inputs[0][1].Role != schema.System || model.inputs[0][2].Role != schema.User {
		t.Fatalf("model input order = %#v", model.inputs)
	}
	fixed := model.inputs[0][0].Content
	for _, want := range []string{"渗透测试智能体", "会话上下文存在 PentGo 技能目录", "调用 load_skill", "不得猜测技能名称"} {
		if !strings.Contains(fixed, want) {
			t.Fatalf("fixed prompt missing %q: %q", want, fixed)
		}
	}
	if strings.Contains(fixed, "`api`：API routing") || strings.Contains(fixed, "/load_skill") || strings.Contains(fixed, "C"+"TF") {
		t.Fatalf("fixed prompt unexpectedly repeats catalog or CLI command: %q", fixed)
	}
	if !strings.Contains(model.inputs[0][1].Content, "`api`：API routing") {
		t.Fatalf("session catalog was not replayed: %#v", model.inputs)
	}
}

func TestEinoEngineDoesNotPersistDomainState(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{schema.AssistantMessage("一次响应", nil)}}
	engine, err := NewEngine(context.Background(), model, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{SessionID: "session-fixture", Messages: []agent.Message{{Role: agent.RoleUser, Content: "任务"}}})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(model.inputs) != 1 || len(model.inputs[0]) != 2 {
		t.Fatalf("engine input = %#v", model.inputs)
	}
}

func TestEinoEngineRegistersFilesystemToolsWithEvidence(t *testing.T) {
	root := t.TempDir()
	workspace, err := builtins.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{messages: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file_path":"notes/result.txt","content":"recorded"}`}}}),
		schema.AssistantMessage("已完成", nil),
	}}
	var calls []string
	engine, err := NewEngine(context.Background(), model, nil, workspace, func(_ context.Context, name string, _ map[string]any, success bool, output string) (string, error) {
		if !success {
			t.Fatalf("filesystem tool %s failed: %s", name, output)
		}
		calls = append(calls, name)
		return output + "\n[evidence_ref: 1]", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "写入文件"}}})
	if err != nil {
		t.Fatal(err)
	}
	var messages []agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Message.Role != "" {
			messages = append(messages, event.Message)
		}
	}
	if len(calls) != 1 || calls[0] != "write_file" {
		t.Fatalf("recorded tools = %#v", calls)
	}
	handlers, err := engine.handlers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runContext := &adk.ChatModelAgentContext{}
	for _, handler := range handlers {
		_, runContext, err = handler.BeforeAgent(context.Background(), runContext)
		if err != nil {
			t.Fatal(err)
		}
	}
	names := make(map[string]bool, len(runContext.Tools))
	for _, tool := range runContext.Tools {
		info, infoErr := tool.Info(context.Background())
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		names[info.Name] = true
	}
	for _, name := range []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute"} {
		if !names[name] {
			t.Fatalf("filesystem tool %q missing from %#v", name, names)
		}
	}
	if content, readErr := os.ReadFile(filepath.Join(root, "notes", "result.txt")); readErr != nil || string(content) != "recorded" {
		t.Fatalf("content/error = %q/%v", content, readErr)
	}
	if len(messages) < 2 || messages[1].Role != agent.RoleTool || !strings.Contains(messages[1].Content, "evidence_ref: 1") {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestEinoEngineRejectsBuiltinToolNameCollision(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{schema.AssistantMessage("unused", nil)}}
	engine, err := NewEngine(context.Background(), model, []agent.Tool{&namedFixtureTool{name: "execute"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "TARGET"}}}); err == nil {
		t.Fatal("built-in name collision succeeded")
	}
}

type namedFixtureTool struct{ name string }

func (tool *namedFixtureTool) Name() string                                      { return tool.name }
func (*namedFixtureTool) Description() string                                    { return "fixture" }
func (*namedFixtureTool) Invoke(context.Context, map[string]any) (string, error) { return "", nil }
