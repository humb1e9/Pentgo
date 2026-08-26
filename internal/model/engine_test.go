package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"pentgo/internal/core"

	"github.com/anthropics/anthropic-sdk-go"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedModel struct {
	chunks    []*schema.Message
	streamErr error
	recvErr   error
	inputs    [][]*schema.Message
	options   [][]model.Option
	toolInfos [][]*schema.ToolInfo
}

func (*scriptedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate must not be called by StreamStep")
}

func (fixture *scriptedModel) Stream(_ context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	fixture.inputs = append(fixture.inputs, append([]*schema.Message(nil), input...))
	fixture.options = append(fixture.options, append([]model.Option(nil), options...))
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
	events, err := engine.StreamStep(context.Background(), core.ModelStepInput{Messages: []core.Message{{Role: core.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []core.Message
	var finals []core.Message
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
	if len(finals) != 1 || finals[0].Role != core.RoleAssistant || finals[0].Content != "你好，世界" || finals[0].ReasoningContent != "先思考" {
		t.Fatalf("finals = %#v", finals)
	}
}

func TestStepPreservesProviderFinishReason(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{{Role: schema.Assistant, Content: "partial", ResponseMeta: &schema.ResponseMeta{FinishReason: "length"}}}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := engine.StreamStep(context.Background(), core.ModelStepInput{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream {
		if event.Final != nil && event.FinishReason != "length" {
			t.Fatalf("finish reason = %q", event.FinishReason)
		}
	}
}

func TestStepRequestsConfiguredOutputTokenCap(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{schema.AssistantMessage("summary", nil)}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := engine.StreamStep(context.Background(), core.ModelStepInput{MaxOutputTokens: 73})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	if len(fixture.options) != 1 {
		t.Fatalf("stream options = %#v", fixture.options)
	}
	options := model.GetCommonOptions(nil, fixture.options[0]...)
	if options.MaxTokens == nil || *options.MaxTokens != 73 {
		t.Fatalf("max tokens = %#v", options.MaxTokens)
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
	events, err := engine.StreamStep(context.Background(), core.ModelStepInput{
		Messages: []core.Message{{Role: core.RoleUser, Content: "check target"}},
		Tools:    []core.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	var final *core.Message
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
	engine, err := NewEngine(context.Background(), fixture, []core.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), core.ModelStepInput{Messages: []core.Message{{Role: core.RoleUser, Content: "call tool"}}})
	if err != nil {
		t.Fatal(err)
	}
	var final *core.Message
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
	events, err := engine.StreamStep(context.Background(), core.ModelStepInput{Messages: []core.Message{{Role: core.RoleUser, Content: "hello"}}})
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
			if !errors.Is(event.Err, core.ErrContextWindowExceeded) {
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
	_, err = engine.StreamStep(context.Background(), core.ModelStepInput{Messages: []core.Message{{Role: core.RoleUser, Content: "hello"}}})
	if !errors.Is(err, core.ErrContextWindowExceeded) {
		t.Fatalf("error = %v, want context-window sentinel", err)
	}
}

func TestNormalizeContextWindowErrorLeavesUnknownErrorsUntouched(t *testing.T) {
	original := &einoopenai.APIError{Code: "invalid_request_error", Message: "unsupported option"}
	if got := normalizeContextWindowError(original); got != original || errors.Is(got, core.ErrContextWindowExceeded) {
		t.Fatalf("normalized unknown error = %v", got)
	}
}

func TestNormalizeContextWindowErrorLeavesUnstructuredOverflowTextUntouched(t *testing.T) {
	original := errors.New(`create new streaming message fail: {"error":{"type":"invalid_request_error","message":"prompt is too long: maximum context length exceeded"}}`)
	if got := normalizeContextWindowError(original); got != original || errors.Is(got, core.ErrContextWindowExceeded) {
		t.Fatalf("normalized unstructured error = %v", got)
	}
}

func newAnthropicAPIError(t *testing.T, message string) *anthropic.Error {
	t.Helper()
	var apiErr anthropic.Error
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":%q}}`, message)), &apiErr); err != nil {
		t.Fatal(err)
	}
	apiErr.Request = &http.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: "api.anthropic.test"}}
	apiErr.Response = &http.Response{StatusCode: http.StatusBadRequest}
	return &apiErr
}

func TestNormalizeContextWindowErrorAcceptsStructuredAnthropicOverflow(t *testing.T) {
	original := newAnthropicAPIError(t, "prompt is too long: maximum context length exceeded")
	if got := normalizeContextWindowError(original); !errors.Is(got, core.ErrContextWindowExceeded) {
		t.Fatalf("structured Anthropic error = %v", got)
	}
}

func TestNormalizeContextWindowErrorRejectsStructuredNonOverflow(t *testing.T) {
	original := newAnthropicAPIError(t, "maximum context length parameter is invalid")
	if got := normalizeContextWindowError(original); got != original || errors.Is(got, core.ErrContextWindowExceeded) {
		t.Fatalf("structured non-overflow error = %v", got)
	}
}

func TestStepUsesChineseSystemPromptAndReplaysMessages(t *testing.T) {
	fixture := &scriptedModel{chunks: []*schema.Message{schema.AssistantMessage("收到", nil)}}
	engine, err := NewEngine(context.Background(), fixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.StreamStep(context.Background(), core.ModelStepInput{Messages: []core.Message{
		{Role: core.RoleSystem, Content: `<pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">` + "\n- `api`：API routing\n</pentgo-skill-catalog>"},
		{Role: core.RoleUser, Content: "检查 API"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(fixture.inputs) != 1 || len(fixture.inputs[0]) != 2 || fixture.inputs[0][0].Role != schema.System || fixture.inputs[0][1].Role != schema.User {
		t.Fatalf("model input order = %#v", fixture.inputs)
	}
	fixed := fixture.inputs[0][0].Content
	for _, want := range []string{"渗透测试智能体", "会话上下文存在 PentGo 技能目录", "调用 load_skill", "不得猜测技能名称"} {
		if !strings.Contains(fixed, want) {
			t.Fatalf("fixed prompt missing %q: %q", want, fixed)
		}
	}
	if !strings.Contains(fixed, "`api`：API routing") {
		t.Fatalf("catalog was not merged into provider system prompt: %q", fixed)
	}
}

func TestMessageConversionPreservesReasoningAndRawArguments(t *testing.T) {
	replayed := toSchemaMessages([]core.Message{{Role: core.RoleAssistant, ReasoningContent: "reason replay", ToolCalls: []core.ToolCall{{ID: "id", Name: "probe", RawArguments: "{malformed"}}}})
	if len(replayed) != 1 || replayed[0].ReasoningContent != "reason replay" || replayed[0].ToolCalls[0].Function.Arguments != "{malformed" {
		t.Fatalf("replayed messages = %#v", replayed)
	}
	converted := fromSchemaMessage(schema.AssistantMessage("", []schema.ToolCall{{ID: "id", Function: schema.FunctionCall{Name: "probe", Arguments: "{malformed"}}}))
	if len(converted.ToolCalls) != 1 || converted.ToolCalls[0].RawArguments != "{malformed" || converted.ToolCalls[0].Arguments == nil {
		t.Fatalf("converted = %#v", converted)
	}
}
