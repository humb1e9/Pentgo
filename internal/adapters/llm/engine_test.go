package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"pentgo/internal/agent"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedModel struct {
	chunks    []*schema.Message
	streamErr error
	recvErr   error
	inputs    [][]*schema.Message
	toolInfos [][]*schema.ToolInfo
}

func (*scriptedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called by StreamStep")
}

func (fixture *scriptedModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	fixture.inputs = append(fixture.inputs, append([]*schema.Message(nil), input...))
	if fixture.streamErr != nil {
		return nil, fixture.streamErr
	}
	if fixture.recvErr == nil {
		return schema.StreamReaderFromArray(fixture.chunks), nil
	}
	reader, writer := schema.Pipe[*schema.Message](len(fixture.chunks) + 1)
	go func() {
		defer writer.Close()
		for _, chunk := range fixture.chunks {
			writer.Send(chunk, nil)
		}
		writer.Send(nil, fixture.recvErr)
	}()
	return reader, nil
}

func (fixture *scriptedModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	fixture.toolInfos = append(fixture.toolInfos, append([]*schema.ToolInfo(nil), infos...))
	return fixture, nil
}

type engineFixtureTool struct {
	invoked bool
}

func (*engineFixtureTool) Name() string        { return "fixture_probe" }
func (*engineFixtureTool) Description() string { return "执行 fixture 检查。" }
func (*engineFixtureTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}}
}
func (fixture *engineFixtureTool) Invoke(context.Context, map[string]any) (string, error) {
	fixture.invoked = true
	panic("StreamStep must not invoke tools")
}

func TestStepStreamsTextThenReturnsOneCompleteAssistantMessage(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{
		{Role: schema.Assistant, Content: "你好", ReasoningContent: "先"},
		{Role: schema.Assistant, Content: "，世界", ReasoningContent: "思考"},
	}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), agent.ModelStepInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []agent.Message
	var finals []agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Delta.Role != "" {
			deltas = append(deltas, event.Delta)
		}
		if event.Final != nil {
			finals = append(finals, *event.Final)
		}
	}
	if len(deltas) != 2 || deltas[0].Content != "你好" || deltas[1].Content != "，世界" {
		t.Fatalf("deltas = %#v", deltas)
	}
	if len(finals) != 1 || finals[0].Role != agent.RoleAssistant || finals[0].Content != "你好，世界" || finals[0].ReasoningContent != "先思考" {
		t.Fatalf("finals = %#v", finals)
	}
}

func TestStepBindsToolsAndPreservesCompleteToolCalls(t *testing.T) {
	index := 0
	fixture := &scriptedModel{chunks: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, ID: "call-1", Function: schema.FunctionCall{Name: "fixture_probe", Arguments: `{"value":"tar`}}}},
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Arguments: `get"}`}}}},
	}}
	tool := &engineFixtureTool{}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), agent.ModelStepInput{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: "check target"}},
		Tools:    []agent.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	var final *agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Final != nil {
			final = event.Final
		}
	}
	if len(fixture.toolInfos) != 1 || len(fixture.toolInfos[0]) != 1 || fixture.toolInfos[0][0].Name != "fixture_probe" {
		t.Fatalf("bound tools = %#v", fixture.toolInfos)
	}
	if final == nil || len(final.ToolCalls) != 1 {
		t.Fatalf("final = %#v", final)
	}
	call := final.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "fixture_probe" || call.RawArguments != `{"value":"target"}` || call.Arguments["value"] != "target" {
		t.Fatalf("tool call = %#v", call)
	}
}

func TestStepDoesNotExecuteTools(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "fixture_probe", Arguments: `{"value":"x"}`}}}),
	}}
	tool := &engineFixtureTool{}
	engine, err := NewEngine(context.Background(), fixture, []agent.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), agent.ModelStepInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "call tool"}}})
	if err != nil {
		t.Fatal(err)
	}
	var final *agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Final != nil {
			final = event.Final
		}
	}
	if tool.invoked {
		t.Fatal("tool was invoked inside model adapter")
	}
	if final == nil || len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "fixture_probe" {
		t.Fatalf("final = %#v", final)
	}
}

func TestStepTransportsAsynchronousStreamErrors(t *testing.T) {
	fixture := &scriptedModel{
		chunks:  []*schema.Message{schema.AssistantMessage("partial", nil)},
		recvErr: &einoopenai.APIError{Code: "context_length_exceeded", Message: "too many tokens"},
	}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), agent.ModelStepInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas, finals, streamErrors int
	for event := range events {
		if event.Delta.Role != "" {
			deltas++
		}
		if event.Final != nil {
			finals++
		}
		if event.Err != nil {
			streamErrors++
			if !errors.Is(event.Err, agent.ErrContextWindowExceeded) {
				t.Fatalf("stream error = %v", event.Err)
			}
		}
	}
	if deltas != 1 || finals != 0 || streamErrors != 1 {
		t.Fatalf("deltas/finals/errors = %d/%d/%d", deltas, finals, streamErrors)
	}
}

func TestStepNormalizesConfiguredContextOverflow(t *testing.T) {
	fixture := &scriptedModel{streamErr: &einoopenai.APIError{Code: "context_length_exceeded", Message: "too many tokens"}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.StreamStep(context.Background(), agent.ModelStepInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}})
	if !errors.Is(err, agent.ErrContextWindowExceeded) {
		t.Fatalf("error = %v, want context-window sentinel", err)
	}
}

func TestNormalizeContextWindowErrorLeavesUnknownErrorsUntouched(t *testing.T) {
	original := &einoopenai.APIError{Code: "invalid_request_error", Message: "unsupported option"}
	if got := normalizeContextWindowError(original); got != original || errors.Is(got, agent.ErrContextWindowExceeded) {
		t.Fatalf("normalized unknown error = %v", got)
	}
}

func TestNormalizeContextWindowErrorLeavesUnstructuredOverflowTextUntouched(t *testing.T) {
	original := errors.New(`create new streaming message fail: {"error":{"type":"invalid_request_error","message":"prompt is too long: maximum context length exceeded"}}`)
	if got := normalizeContextWindowError(original); got != original || errors.Is(got, agent.ErrContextWindowExceeded) {
		t.Fatalf("normalized unstructured error = %v", got)
	}
}

func TestStepUsesChineseSystemPromptAndReplaysMessages(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{schema.AssistantMessage("收到", nil)}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), agent.ModelStepInput{Messages: []agent.Message{
		{Role: agent.RoleSystem, Content: `<pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">` + "\n- `api`：API routing\n</pentgo-skill-catalog>"},
		{Role: agent.RoleUser, Content: "检查 API"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(fixture.inputs) != 1 || len(fixture.inputs[0]) != 3 || fixture.inputs[0][0].Role != schema.System || fixture.inputs[0][1].Role != schema.System || fixture.inputs[0][2].Role != schema.User {
		t.Fatalf("model input order = %#v", fixture.inputs)
	}
	fixed := fixture.inputs[0][0].Content
	for _, want := range []string{"渗透测试智能体", "会话上下文存在 PentGo 技能目录", "调用 load_skill", "不得猜测技能名称"} {
		if !strings.Contains(fixed, want) {
			t.Fatalf("fixed prompt missing %q: %q", want, fixed)
		}
	}
	if strings.Contains(fixed, "`api`：API routing") || strings.Contains(fixed, "/load_skill") || strings.Contains(fixed, "C"+"TF") {
		t.Fatalf("fixed prompt unexpectedly repeats catalog or CLI command: %q", fixed)
	}
	if !strings.Contains(fixture.inputs[0][1].Content, "`api`：API routing") {
		t.Fatalf("session catalog was not replayed: %#v", fixture.inputs)
	}
}

func TestMessageConversionPreservesReasoningAndRawArguments(t *testing.T) {
	replayed := toSchemaMessages([]agent.Message{{Role: agent.RoleAssistant, ReasoningContent: "reason replay", ToolCalls: []agent.ToolCall{{ID: "id", Name: "probe", RawArguments: "{malformed"}}}})
	if len(replayed) != 1 || replayed[0].ReasoningContent != "reason replay" || replayed[0].ToolCalls[0].Function.Arguments != "{malformed" {
		t.Fatalf("replayed messages = %#v", replayed)
	}
	converted := fromSchemaMessage(schema.AssistantMessage("", []schema.ToolCall{{ID: "id", Function: schema.FunctionCall{Name: "probe", Arguments: "{malformed"}}}))
	if len(converted.ToolCalls) != 1 || converted.ToolCalls[0].RawArguments != "{malformed" || converted.ToolCalls[0].Arguments == nil {
		t.Fatalf("converted = %#v", converted)
	}
}
