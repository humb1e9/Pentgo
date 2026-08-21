# 最终回答流式输出 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 PentGo 在 TUI 中实时显示最终回答文本，同时只将完整最终消息写入 transcript，并在非流式模型或中断情形下安全降级。

**Architecture:** 在 `agent.TurnEvent` 增加 transient `text_delta`，由 Eino Engine 从 ADK stream 读取 chunk、实时发出 delta 并聚合为一条最终 `message`。`TurnService` 仅转发 delta 到 UI event，不持久化它；现有完整 message 分支继续作为 transcript、证据和会话总结的唯一事实源。CLI 为当前会话维护可丢弃的流式草稿，最终 transcript 消息到达时原子替换草稿。

**Tech Stack:** Go 1.25、Eino v0.9.13 ADK/schema stream reader、Bubble Tea v1.3、Lip Gloss v1.1、SQLite transcript persistence。

## Global Constraints

- 只流式显示最终回答文本，不显示或持久化 reasoning、草稿或模型内部思考。
- `text_delta` 只能在 Engine → TurnService → ProjectRuntime → CLI 的内存事件流中存在，绝不能调用 `TranscriptStore.Append`。
- transcript、evidence、历史回放和 session lifecycle 的既有语义不变；完整 `TurnEventMessage` 仍是唯一持久化 assistant 输出。
- 保持现有工具状态块和 `Ctrl+O` 工具详情交互；工具调用/结果与最终回答按 event 顺序显示。
- 如果 streaming runner 收到一个完整消息而没有 delta，正常显示“正在生成…”后完整回复；如果在尚未产生任何消息前报明确的“stream unsupported”错误，自动以 `EnableStreaming:false` 重跑一次。
- 对已经输出任何消息/文本后的错误不重跑，避免重复工具调用或重复回答。
- 中断/失败时：有文本的流式草稿留在当前屏幕但不写 transcript；无文本的“正在生成…”占位消失；新建、删除、清除或失焦会话时清理草稿。
- 所有增量和最终 UI 文本都通过现有 `safeTerminalText`；ANSI/OSC/控制字符不能到达终端。
- 高频 delta 是可丢失 UI 进度事件；Engine 内部聚合与最终完整消息绝不能受 UI channel 丢弃影响。
- 不改 provider 配置、数据库 schema、MCP 接口或用户输入命令。
- 运行 `go test ./... -count=1`、`go test -race ./...`、`go vet ./...`、`go build ./cmd/...` 与 `git diff --check`。

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/agent/turn.go` | 定义 provider-neutral transient `TurnEventTextDelta` 和 `TurnEvent.Delta`。 |
| `internal/adapters/llm/engine.go` | 启用 ADK streaming、消费 `MessageStream`、立即发送回答文本 delta、聚合完整 schema message，并安全回退到非流 runner。 |
| `internal/adapters/llm/engine_test.go` | 以可控的 streaming/unsupported fixture model 验证 engine delta 顺序、完整聚合和 fallback。 |
| `internal/app/events.go` | 定义 CLI 面向的 `EventAssistantDelta`。 |
| `internal/app/turn_service.go` | 转发 delta 而不写 transcript；完整 message 的既有 append/persist 分支不变。 |
| `internal/app/turn_service_test.go` | 验证 delta 已发出、持久化 transcript 无 delta、取消时无部分 assistant message。 |
| `internal/cli/model.go` | 保存当前会话的 transient stream draft，并将其渲染为 `PENTGO · 正在生成…`。 |
| `internal/cli/model_test.go` | 验证 draft 累积、最终替换、fallback 占位、失败保留部分文本、会话边界清理和控制字符过滤。 |

`internal/cli/model.go` 与 `internal/cli/model_test.go` 在本会话开始时已经是未跟踪文件；实施期间只能对其进行路径限定的检查，不应自动把整文件提交，除非所有者另行明确批准。

### Task 1: 扩展无持久化的流式事件协议

**Files:**
- Modify: `internal/agent/turn.go:5-58`
- Modify: `internal/app/events.go:14-22`
- Test: `internal/app/turn_service_test.go`

**Interfaces:**
- Produces: `agent.TurnEventTextDelta string`；`agent.TurnEvent{Kind: agent.TurnEventTextDelta, Delta: string}`；`app.EventAssistantDelta string`。
- Consumes: 既有 `agent.TurnEventMessage`、`TurnEventError`、`TurnEventDone` 和 `app.Event`。
- Contract: `Delta` is never a durable `agent.Message`, and `EventAssistantDelta.Message` is one display chunk.

- [ ] **Step 1: 写出 TurnService 不持久化 delta 的失败测试**

在 `internal/app/turn_service_test.go` 的 `TestTurnPersistsUserToolAssistantMessages` 后添加：

```go
func TestTurnPublishesDeltasWithoutPersistingThem(t *testing.T) {
	engine := scriptedEngine{run: func(context.Context, agent.TurnInput) []agent.TurnEvent {
		return []agent.TurnEvent{
			{Kind: agent.TurnEventTextDelta, Delta: "正在"},
			{Kind: agent.TurnEventTextDelta, Delta: "流式回答"},
			{Kind: agent.TurnEventMessage, Message: agent.Message{Role: agent.RoleAssistant, Content: "正在流式回答"}},
		}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, engine)
	events := projectRuntime.Events(session.ID)
	if err := <-projectRuntime.Submit(context.Background(), session.ID, "检查"); err != nil {
		t.Fatal(err)
	}

	var deltas []string
	for {
		select {
		case event := <-events:
			if event.Kind == EventAssistantDelta {
				deltas = append(deltas, event.Message)
			}
		case <-time.After(50 * time.Millisecond):
			goto collected
		}
	}
collected:
	if got := strings.Join(deltas, ""); got != "正在流式回答" {
		t.Fatalf("deltas = %q", got)
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 2 || messages[1].Role != agent.RoleAssistant || messages[1].Content != "正在流式回答" {
		t.Fatalf("transcript = %#v", messages)
	}
}
```

Use the existing test file imports (`strings`, `time`) rather than adding a new dependency.

- [ ] **Step 2: 运行测试确认缺少事件类型和字段**

Run:

```bash
go test ./internal/app -run TestTurnPublishesDeltasWithoutPersistingThem -count=1
```

Expected: FAIL to compile because `TurnEventTextDelta`, `Delta`, and `EventAssistantDelta` do not yet exist.

- [ ] **Step 3: 定义 provider-neutral delta 和 CLI event 常量**

In `internal/agent/turn.go`, replace the event constants and `TurnEvent` definition with:

```go
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	TurnEventMessage   = "message"
	TurnEventTextDelta = "text_delta"
	TurnEventError     = "error"
	TurnEventDone      = "done"
)

type TurnEvent struct {
	Kind    string
	Message Message
	Delta   string
	Tool    string
	Output  string
	Err     error
}
```

In `internal/app/events.go`, insert the UI event between `EventTurnStarted` and tool events:

```go
EventAssistantDelta = "assistant_delta"
```

The comment for `TurnEvent` must explicitly state that `TextDelta` is transient and never a transcript message.

- [ ] **Step 4: 转发 delta，并跳过 append/persist**

In `TurnService.RunTurn`, add this branch immediately after the current error branch and before `if event.Message.Role == ""`:

```go
if event.Kind == agent.TurnEventTextDelta {
	delta := strings.TrimSpace(event.Delta)
	if delta != "" {
		projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventAssistantDelta, Message: event.Delta})
	}
	continue
}
```

Do not trim the actual emitted `event.Delta`: whitespace-only chunks are skipped, but leading/trailing whitespace in a nonempty text chunk must be preserved so `"hello"` + `" world"` stays accurate. This branch must execute before the role-empty skip and must not call `transcript.Append`, `persist`, or `PublishSnapshot`.

- [ ] **Step 5: 运行应用测试**

Run:

```bash
gofmt -w internal/agent/turn.go internal/app/events.go internal/app/turn_service.go internal/app/turn_service_test.go
go test ./internal/app -run 'TestTurn(PublishesDeltasWithoutPersistingThem|PersistsUserToolAssistantMessages|DoneLeavesSessionReusable)' -count=1
```

Expected: PASS.

- [ ] **Step 6: 记录提交边界**

Run:

```bash
git status --short -- internal/agent/turn.go internal/app/events.go internal/app/turn_service.go internal/app/turn_service_test.go
```

If these files are tracked and clean before this task, commit only these paths:

```bash
git add internal/agent/turn.go internal/app/events.go internal/app/turn_service.go internal/app/turn_service_test.go
git commit -m "feat: publish transient assistant text deltas"
```

If any listed file already contains unrelated work, do not stage it; report the exact conflicting path and keep the verified change uncommitted.

### Task 2: 将 Eino ADK stream 聚合为 delta 与完整消息

**Files:**
- Modify: `internal/adapters/llm/engine.go:3-163, 308-329`
- Modify: `internal/adapters/llm/engine_test.go:19-40, 53-112`

**Interfaces:**
- Consumes: Task 1 `agent.TurnEventTextDelta` and `TurnEvent.Delta`; Eino `adk.MessageVariant{IsStreaming, MessageStream, Role}`; `schema.StreamReader.Recv/Close` and `schema.ConcatMessages`.
- Produces: `consumeMessageOutput(ctx context.Context, variant *adk.MessageVariant, emit func(agent.TurnEvent) bool) (*schema.Message, error)` and `isStreamingUnsupported(error) bool`; Engine always emits exactly one complete `TurnEventMessage` for every valid ADK message output.
- Contract: Only assistant `chunk.Content` emits delta; `ReasoningContent` never emits delta. The full concatenated message retains tool calls/results and is sent after all its deltas.

- [ ] **Step 1: 添加 streaming fixture 与失败测试**

In `internal/adapters/llm/engine_test.go`, extend imports with `"io"` only if the fixture needs it. Add this model after `scriptedModel`:

```go
type streamedModel struct {
	chunks [][]*schema.Message
	inputs [][]*schema.Message
}

func (model *streamedModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, fmt.Errorf("Generate should not be called")
}

func (model *streamedModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	model.inputs = append(model.inputs, append([]*schema.Message(nil), input...))
	if len(model.chunks) == 0 {
		return nil, fmt.Errorf("stream exhausted")
	}
	chunks := model.chunks[0]
	model.chunks = model.chunks[1:]
	return schema.StreamReaderFromArray(chunks), nil
}

func (model *streamedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return model, nil }
```

Then add:

```go
func TestEinoEngineStreamsAssistantDeltasThenCompleteMessage(t *testing.T) {
	model := &streamedModel{chunks: [][]*schema.Message{{
		schema.AssistantMessage("实时", nil),
		schema.AssistantMessage("回答", nil),
	}}}
	engine, err := NewEngine(context.Background(), model, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "检查"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var complete []agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Kind == agent.TurnEventTextDelta {
			deltas = append(deltas, event.Delta)
		}
		if event.Kind == agent.TurnEventMessage && event.Message.Role != "" {
			complete = append(complete, event.Message)
		}
	}
	if got := strings.Join(deltas, ""); got != "实时回答" {
		t.Fatalf("deltas = %q", got)
	}
	if len(complete) != 1 || complete[0].Role != agent.RoleAssistant || complete[0].Content != "实时回答" {
		t.Fatalf("complete messages = %#v", complete)
	}
}

func TestEinoEngineFallsBackWhenStreamingIsUnsupported(t *testing.T) {
	model := &scriptedModel{messages: []*schema.Message{schema.AssistantMessage("完整回退回答", nil)}}
	engine, err := NewEngine(context.Background(), model, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := engine.Run(context.Background(), agent.TurnInput{Messages: []agent.Message{{Role: agent.RoleUser, Content: "检查"}}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var complete []agent.Message
	for event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Kind == agent.TurnEventTextDelta {
			deltas = append(deltas, event.Delta)
		}
		if event.Kind == agent.TurnEventMessage && event.Message.Role != "" {
			complete = append(complete, event.Message)
		}
	}
	if len(deltas) != 0 || len(complete) != 1 || complete[0].Content != "完整回退回答" {
		t.Fatalf("deltas/messages = %#v/%#v", deltas, complete)
	}
}
```

- [ ] **Step 2: 运行测试确认流式实现尚不存在**

Run:

```bash
go test ./internal/adapters/llm -run 'TestEinoEngine(StreamsAssistantDeltasThenCompleteMessage|FallsBackWhenStreamingIsUnsupported)' -count=1
```

Expected: FAIL because the runner is still configured with `EnableStreaming: false` and `scriptedModel.Stream` failure propagates instead of falling back.

- [ ] **Step 3: 实现消息 output 消费和安全 fallback**

In `internal/adapters/llm/engine.go`, add imports `errors`, `io`, and `strings` only if not already present. Add the following helpers below `Run`:

```go
func consumeMessageOutput(ctx context.Context, variant *adk.MessageVariant, emit func(agent.TurnEvent) bool) (*schema.Message, error) {
	if variant == nil {
		return nil, nil
	}
	if !variant.IsStreaming {
		return variant.GetMessage()
	}
	if variant.MessageStream == nil {
		return nil, fmt.Errorf("streaming message output has no stream")
	}
	stream := variant.MessageStream
	defer stream.Close()
	chunks := make([]*schema.Message, 0, 16)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		role := variant.Role
		if role == "" {
			role = chunk.Role
		}
		if role == schema.Assistant && chunk.Content != "" && !emit(agent.TurnEvent{Kind: agent.TurnEventTextDelta, Delta: chunk.Content}) {
			return nil, ctx.Err()
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	return schema.ConcatMessages(chunks)
}

func isStreamingUnsupported(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "stream") && (strings.Contains(text, "unsupported") || strings.Contains(text, "not supported") || strings.Contains(text, "unimplemented"))
}
```

Replace the single runner setup with two runners:

```go
streamingRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: modelAgent, EnableStreaming: true})
fallbackRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: modelAgent, EnableStreaming: false})
iterator := streamingRunner.Run(ctx, toSchemaMessages(input.Messages))
```

In the goroutine, add `usedFallback := false` and `emittedOutput := false`. Replace `GetMessage()` with `consumeMessageOutput`. When an `event.Err` occurs before `emittedOutput`, and `!usedFallback && isStreamingUnsupported(event.Err)`, set `usedFallback = true`, replace `iterator` with `fallbackRunner.Run(ctx, toSchemaMessages(input.Messages))`, and `continue`; otherwise preserve the existing error event path. Mark `emittedOutput = true` immediately before emitting any text delta, complete message, or error that is not eligible for fallback. Use an `emit` closure which select-sends to `output` and returns false when `ctx.Done()` closes.

A non-streaming `MessageOutput` from a streaming runner must go through `consumeMessageOutput` unchanged; it emits no delta and produces one normal complete message. Do not rerun on any error after the first emitted output.

- [ ] **Step 4: 将流消费接入原有完整消息发送路径**

Replace lines that directly call `event.Output.MessageOutput.GetMessage()` with:

```go
message, err := consumeMessageOutput(ctx, event.Output.MessageOutput, emit)
if err != nil || message == nil {
	if err != nil {
		emit(agent.TurnEvent{Kind: agent.TurnEventError, Err: err, Output: err.Error()})
	}
	return
}
emittedOutput = true
converted := fromSchemaMessage(message)
if !emit(agent.TurnEvent{Kind: agent.TurnEventMessage, Message: converted, Output: assistantSummary(message.Content)}) {
	return
}
```

The `emit` closure must be the sole write path to `output`, preserving cancellation behavior. Do not include `schema.Message.ReasoningContent` in a `TurnEventTextDelta`; `fromSchemaMessage` should continue using only `Content` as the user-visible assistant content.

- [ ] **Step 5: 运行 adapter 测试和既有工具回归**

Run:

```bash
gofmt -w internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go
go test ./internal/adapters/llm -count=1
```

Expected: PASS, including existing tool call/result and filesystem evidence tests. The old `scriptedModel` should exercise the fallback path because its `Stream` returns `streaming unsupported` before output.

- [ ] **Step 6: 记录提交边界**

Run:

```bash
git status --short -- internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go
```

If these paths contain only this task’s tracked changes, run:

```bash
git add internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go
git commit -m "feat(llm): stream final response text"
```

Otherwise leave files unstaged and report the conflicting path.

### Task 3: 在 TUI 中累积、替换和丢弃流式草稿

**Files:**
- Modify: `internal/cli/model.go:73-90, 285-302, 533-587`
- Modify: `internal/cli/model_test.go`

**Interfaces:**
- Consumes: Task 1 `app.EventAssistantDelta`; existing `EventTurnStarted`, `EventAssistantMessage`, `EventToolStarted`, `EventTurnFinished`, `EventTurnFailed`; existing `safeTerminalText` and `renderTranscriptBlock`.
- Produces: `terminalModel.streamDraft string`, `streamVisible bool`, `streamGenerating bool`; `renderStreamDraft(width int) string`; `clearStreamDraft()`.
- Contract: final transcript assistant event replaces the draft; failed/interrupted turns retain nonempty text but remove the generating label; session boundaries clear all draft fields.

- [ ] **Step 1: 写出 TUI stream 状态的失败测试**

Add these tests to `internal/cli/model_test.go`:

```go
func TestTerminalModelAccumulatesAndReplacesStreamDraft(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	model.recordEvent(app.Event{Kind: app.EventAssistantDelta, Message: "实时"})
	model.recordEvent(app.Event{Kind: app.EventAssistantDelta, Message: "回答"})
	if !model.streamVisible || !model.streamGenerating || model.streamDraft != "实时回答" {
		t.Fatalf("stream state = visible:%t generating:%t draft:%q", model.streamVisible, model.streamGenerating, model.streamDraft)
	}
	view := ansi.Strip(model.renderStreamDraft(80))
	if !strings.Contains(view, "PENTGO · 正在生成…") || !strings.Contains(view, "实时回答") {
		t.Fatalf("draft view = %q", view)
	}
	model.recordEvent(app.Event{Kind: app.EventAssistantMessage, Message: "实时回答"})
	if model.streamVisible || model.streamGenerating || model.streamDraft != "" {
		t.Fatalf("final message did not replace draft: %+v", model)
	}
}

func TestTerminalModelKeepsPartialDraftOnlyForCurrentScreenAfterInterrupt(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	model.recordEvent(app.Event{Kind: app.EventAssistantDelta, Message: "半截回答"})
	model.recordEvent(app.Event{Kind: app.EventTurnFinished, Message: "turn interrupted"})
	if !model.streamVisible || model.streamGenerating || model.streamDraft != "半截回答" {
		t.Fatalf("interrupted draft = visible:%t generating:%t draft:%q", model.streamVisible, model.streamGenerating, model.streamDraft)
	}
	model.clearTransientState()
	if model.streamVisible || model.streamGenerating || model.streamDraft != "" {
		t.Fatalf("clear leaked stream state: %+v", model)
	}
}

func TestTerminalModelFiltersControlSequencesFromStreamDeltas(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	model.recordEvent(app.Event{Kind: app.EventAssistantDelta, Message: "safe\x1b]52;c;clipboard\x07\x1b[2J text"})
	if strings.ContainsAny(model.streamDraft, "\x1b\a\r") || strings.Contains(model.streamDraft, "clipboard") {
		t.Fatalf("unsafe draft = %q", model.streamDraft)
	}
}
```

- [ ] **Step 2: 运行测试确认状态字段和渲染器不存在**

Run:

```bash
go test ./internal/cli -run 'TestTerminalModel(AccumulatesAndReplacesStreamDraft|KeepsPartialDraftOnlyForCurrentScreenAfterInterrupt|FiltersControlSequencesFromStreamDeltas)' -count=1
```

Expected: FAIL to compile because the stream draft fields and `renderStreamDraft` do not yet exist.

- [ ] **Step 3: 添加 transient stream 状态与渲染块**

Add fields to `terminalModel`:

```go
streamDraft      string
streamVisible    bool
streamGenerating bool
```

In `renderConversation`, append this immediately after transcript messages and before transient activity:

```go
if model.streamVisible {
	lines = append(lines, model.renderStreamDraft(model.contentWidth()))
}
```

Add:

```go
func (model *terminalModel) renderStreamDraft(width int) string {
	title := "PENTGO"
	if model.streamGenerating {
		title += " · 正在生成…"
	}
	content := model.streamDraft
	if strings.TrimSpace(content) == "" {
		content = "..."
	}
	return renderTranscriptBlock(modelStyle.Render(title), content, max(10, width-2), 0, modelBodyStyle)
}

func (model *terminalModel) clearStreamDraft() {
	model.streamDraft = ""
	model.streamVisible = false
	model.streamGenerating = false
}
```

`renderTranscriptBlock` already applies `safeTerminalText`; `recordEvent` must also sanitize each delta before appending to ensure model state never retains terminal control content.

- [ ] **Step 4: 扩展 `recordEvent` 和 transient cleanup**

At the top of `recordEvent`, retain its existing `name := safeTerminalText(event.Message)` and add cases:

```go
case app.EventTurnStarted:
	model.turnRunning = true
	model.clearStreamDraft()
	model.streamVisible = true
	model.streamGenerating = true
case app.EventAssistantDelta:
	delta := safeTerminalText(event.Message)
	if delta != "" {
		model.streamDraft += delta
		model.streamVisible = true
		model.streamGenerating = true
	}
case app.EventAssistantMessage:
	model.clearStreamDraft()
case app.EventToolStarted:
	model.clearStreamDraft()
	// retain the existing runningTools increment
case app.EventTurnFailed:
	model.turnRunning = false
	model.runningTools = make(map[string]int)
	if model.streamDraft == "" {
		model.clearStreamDraft()
	} else {
		model.streamGenerating = false
	}
	model.addActivity(activityError, name)
case app.EventTurnFinished:
	model.turnRunning = false
	model.runningTools = make(map[string]int)
	if name == "turn interrupted" || name == "runtime stopped" {
		if model.streamDraft == "" {
			model.clearStreamDraft()
		} else {
			model.streamGenerating = false
		}
		model.addActivity(activityStatus, name)
	}
```

Keep the existing `EventToolFinished` logic. Update `clearTransientState` to call `model.clearStreamDraft()` in addition to clearing activity, running tools and turn status. Existing `/new`, `/clear`, focus deletion and start-with-message paths already invoke `clearTransientState`, so no separate session map is needed.

- [ ] **Step 5: 运行 CLI 全量测试**

Run:

```bash
gofmt -w internal/cli/model.go internal/cli/model_test.go
go test ./internal/cli -count=1
```

Expected: PASS. Existing welcome/new-session, tool status, control-sequence and Ctrl+O tests must remain green.

- [ ] **Step 6: 记录未跟踪 CLI 文件的提交限制**

Run:

```bash
git status --short -- internal/cli/model.go internal/cli/model_test.go
```

Do not stage or commit these paths automatically because they were already untracked before streaming work. Record the test result in the final delivery instead.

### Task 4: 端到端持久化与质量门禁

**Files:**
- Modify: `internal/app/turn_service_test.go` (only if Task 1 test needs deterministic event draining)
- Modify: `docs/superpowers/plans/2026-08-20-streaming-final-response.md` (check completed steps and append execution record)

**Interfaces:**
- Consumes: all Task 1–3 interfaces.
- Produces: verified streaming delivery; no additional program interfaces.

- [ ] **Step 1: 添加中断后不写半截 assistant 的回归测试**

In `internal/app/turn_service_test.go`, add a cancellation-aware engine fixture that emits one delta, waits for cancellation, and does not emit a complete message:

```go
func TestInterruptedTurnDoesNotPersistPartialAssistantDelta(t *testing.T) {
	deltaSent := make(chan struct{})
	engine := scriptedEngine{run: func(ctx context.Context, _ agent.TurnInput) []agent.TurnEvent {
		close(deltaSent)
		<-ctx.Done()
		return []agent.TurnEvent{{Kind: agent.TurnEventTextDelta, Delta: "不会持久化"}}
	}}
	projectRuntime, session, _ := newApplicationFixture(t, engine)
	ctx, cancel := context.WithCancel(context.Background())
	done := projectRuntime.Submit(ctx, session.ID, "开始")
	select {
	case <-deltaSent:
	case <-time.After(time.Second):
		t.Fatal("engine did not start")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled turn returned nil")
	}
	messages := projectRuntime.Transcript(session.ID).Messages()
	if len(messages) != 1 || messages[0].Role != agent.RoleUser {
		t.Fatalf("partial assistant leaked into transcript: %#v", messages)
	}
}
```

Update the fixture if necessary so it emits the delta before waiting: use a custom `agent.ModelEngine` in this test if the existing `scriptedEngine` cannot observe cancellation while returning its slice. The required assertion is exact: transcript contains only the user message.

- [ ] **Step 2: 运行 focused regression suites**

Run:

```bash
go test ./internal/adapters/llm -count=1
go test ./internal/app -count=1
go test ./internal/cli -count=1
```

Expected: every command exits `0`.

- [ ] **Step 3: 手动验证实际 OpenAI-compatible streaming 请求**

Run:

```bash
go build -o bin/pentgo ./cmd/pentgo
./bin/pentgo
```

In the TUI, submit a short non-tool prompt such as `用一句话说明你已开始流式回答。`.

Expected:

1. Before the first token, `PENTGO · 正在生成…` appears.
2. The final answer grows during generation; it does not wait for the whole completion.
3. When finished, the generating label disappears and exactly one final assistant response remains.
4. Relaunch with `./bin/pentgo resume`; the transcript contains one complete final response, no token fragments.
5. Submit a prompt likely to use a configured tool; tool blocks remain visible and no duplicated stream draft remains after a tool call.

If the configured provider returns a normal full response with no chunks, confirm that the placeholder appears and then is replaced by the full response with no error. If it returns an explicit stream-unsupported error, confirm one automatic non-streaming retry succeeds; do not print API keys or request headers.

- [ ] **Step 4: 运行全量质量门禁**

Run:

```bash
gofmt -w internal/agent/turn.go internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go internal/app/events.go internal/app/turn_service.go internal/app/turn_service_test.go internal/cli/model.go internal/cli/model_test.go
go test ./... -count=1
go test -race ./...
go vet ./...
go build ./cmd/...
git diff --check
git diff --no-index --check /dev/null internal/cli/model.go || test $? -eq 1
git diff --no-index --check /dev/null internal/cli/model_test.go || test $? -eq 1
```

Expected: every command exits `0`; whitespace checks emit no errors.

- [ ] **Step 5: 更新计划与有选择地提交**

Mark completed non-commit steps `- [x]` and append:

```markdown
## Execution Record

- Verified Engine streamed delta ordering, aggregation, unsupported-stream fallback, TurnService non-persistence, CLI draft lifecycle and terminal-control filtering.
- Verified `go test ./... -count=1`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/...`, and whitespace checks.
- `internal/cli/model.go` and `internal/cli/model_test.go` remained uncommitted because they were pre-existing untracked files.
```

Then inspect:

```bash
git status --short
git diff --check
```

Commit only tracked paths proven to contain this feature and no unrelated changes; never use `git add -A`. If no safe tracked-only commit exists, leave the complete verified worktree intact and report the exact files awaiting owner review.

## Self-Review

### Spec coverage

| Approved requirement | Plan task |
| --- | --- |
| Final-answer-only streaming, no reasoning | Task 2 chunk filter and Global Constraints |
| Transient delta protocol | Task 1 |
| Engine streaming, aggregation, full message | Task 2 |
| Non-streaming and unsupported-stream fallback | Task 2 |
| Transcript contains only complete message | Tasks 1 and 4 |
| Tool ordering/unchanged persistence | Tasks 2–3 plus existing adapter tests |
| TUI generating placeholder, draft replacement and autoscroll | Task 3 |
| Interrupted partial only remains on screen | Tasks 3–4 |
| Session boundary cleanup/control sequence safety | Task 3 |
| Full test, race, build and manual provider verification | Task 4 |

### Placeholder scan

No implementation step relies on a future unspecified component. The one custom-cancellation-fixture branch in Task 4 gives the required behavior and exact assertion; it exists only because the current `scriptedEngine` deliberately returns a prebuilt slice and cannot yield a delta before cancellation.

### Type consistency

- Task 1 defines `agent.TurnEventTextDelta`, `TurnEvent.Delta`, and `app.EventAssistantDelta`; Tasks 2 and 3 use these exact names.
- Task 2’s `consumeMessageOutput` emits the Task 1 `TurnEventTextDelta` and still produces existing `TurnEventMessage` values for `TurnService`.
- Task 3 consumes `EventAssistantDelta` through existing `recordEvent`, and uses `clearStreamDraft()` from `clearTransientState`.
- All durable behavior remains keyed to existing `agent.Message` and `TranscriptStore.Append`; no delta has a `Message.Role`, so it cannot enter the persistence branch.
