# Claude Code 风格 TUI 改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变 PentGo 单会话运行架构的前提下，将 Bubble Tea 对话界面升级为具有 Claude Code 式层级、欢迎态、紧凑工具块和 `Ctrl+O` 详情切换的终端体验。

**Architecture:** 所有变更限定在 `internal/cli/model.go` 的临时视图状态与纯渲染辅助函数中。`terminalModel` 新增 `showToolDetails` 与临时活动记录；`renderConversation` 仍只读取 `Coordinator.Messages` 的持久化 transcript，运行时事件仅补充尚未持久化的工具进度和错误。将渲染分成顶部上下文、空态、transcript 工具摘要/详情和输入 composer，统一依据 `contentWidth()` 做 ANSI 安全截断与换行。

**Tech Stack:** Go 1.25、Bubble Tea v1.3、Bubbles textinput/viewport、Lip Gloss v1.1、charmbracelet/x/ansi、Go 标准库 `encoding/json`。

## Global Constraints

- 保持一个 TUI 只承载当前会话；不增加会话侧栏、会话切换或鼠标交互。
- 不改变 `internal/app`、`internal/domain`、`internal/adapters` 的持久化、工具、MCP、模型或 session 业务语义。
- 沿用现有 Enter 提交、Ctrl+C/Ctrl+D 退出、斜杠命令和 viewport 行为。
- 详情开关使用 `Ctrl+O`，作用于当前会话全部工具调用与结果；默认关闭。
- 标准终端色优先；即使剥离 ANSI，所有角色、工具状态与错误仍有明确文字标签。
- 用户、助手、工具调用、工具结果、工具进行中和错误必须有独立可读层级。
- 任何长文本都必须在当前可用宽度下经 ANSI 安全截断或换行；窄终端不得挤压 viewport。
- 全部新增行为以 Go 单元测试覆盖，并运行 `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/...` 与 `git diff --check`。

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/cli/model.go` | 定义 Claude 风格样式、终端临时 UI 状态、`Ctrl+O` 行为、动态布局以及纯渲染辅助函数。 |
| `internal/cli/model_test.go` | 使用真实 Coordinator 验证视图状态、快捷键、欢迎态和转录渲染；对纯渲染函数剥离 ANSI 后断言稳定的文本结构。 |
| `docs/superpowers/specs/2026-08-20-claude-code-tui-refresh.md` | 已批准的产品规格；实施过程中作为范围与验收依据。 |

没有新增包或应用层接口。`app.Event` 和 `agent.Message` 是既有输入契约，CLI 只消费它们。

### Task 1: 建立 Claude 风格视图状态和稳定布局

**Files:**
- Modify: `internal/cli/model.go:25-57, 76-99, 105-169, 193-204`
- Test: `internal/cli/model_test.go`

**Interfaces:**
- Consumes: `terminalModel` 的既有 `width`、`height`、`input`、`viewport`、`project`、`sessions` 和 `focused` 字段。
- Produces: `terminalModel.showToolDetails bool`；`terminalModel.headerHeight() int`、`terminalModel.composerHeight() int`、`terminalModel.renderHeader() string`、`terminalModel.renderComposer() string`；它们供 Task 2 和 Task 3 调用。

- [x] **Step 1: 写出欢迎态、固定 composer 和窄窗口布局的失败测试**

在 `internal/cli/model_test.go` 的现有测试后加入以下测试。它们先只依赖后续会实现的文本标签与布局方法；创建 project/session 的写法与同文件其他测试保持一致：

```go
func TestTerminalModelShowsWelcomeCardForEmptySession(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, session.ID)
	model.width, model.height = 100, 32
	model.layout()
	model.refresh()

	view := ansi.Strip(model.View())
	for _, want := range []string{"PentGo", "准备开始", "/load_skill", "/new", "/help", "Enter 发送", "Ctrl+O 详情"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestTerminalModelLayoutKeepsViewportPositiveInNarrowWindow(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.width, model.height = 24, 10
	model.layout()

	if model.viewport.Width < 12 || model.viewport.Height < 3 {
		t.Fatalf("viewport = %dx%d", model.viewport.Width, model.viewport.Height)
	}
	if model.input.Width < 12 {
		t.Fatalf("input width = %d", model.input.Width)
	}
	if got := model.headerHeight() + model.composerHeight() + model.viewport.Height; got > model.height {
		t.Fatalf("layout height = %d, terminal height = %d", got, model.height)
	}
}
```

- [x] **Step 2: 运行新增测试，确认它们失败**

Run:

```bash
go test ./internal/cli -run 'TestTerminalModelShowsWelcomeCardForEmptySession|TestTerminalModelLayoutKeepsViewportPositiveInNarrowWindow' -count=1
```

Expected: FAIL，因为 `headerHeight` 和 `composerHeight` 尚未定义，且视图还没有 `准备开始`、`Enter 发送` 和 `Ctrl+O 详情` 文案。

- [x] **Step 3: 添加命名化 Claude 风格样式、布局方法和壳层渲染**

在 `internal/cli/model.go`：

1. 用以下样式替换当前 `var (...)` 中的 header/input 视觉定义；保留并继续使用 `mutedStyle`、`activeStyle`、`errorStyle`、`userStyle`、`userBodyStyle`、`modelStyle`、`modelBodyStyle`、`toolStyle`、`toolBodyStyle` 这些名称，但调整为低饱和中性色。新增 `shellStyle`、`brandStyle`、`metaStyle`、`composerHintStyle` 和 `welcomeStyle`。颜色全部使用标准 ANSI 索引。

```go
var (
	shellStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	brandStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	metaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headerStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252")).
		Padding(0, 1)
	inputStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)
	composerHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	welcomeStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Foreground(lipgloss.Color("252")).
		Padding(1, 2)
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("151")).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	userStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
	userBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	modelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	modelBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true)
	toolBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
)
```

2. 在 `terminalModel` 中增加详情状态：

```go
showToolDetails bool
```

3. 在 `newTerminalModel` 中把输入设为 `› `、将 placeholder 改为 `向 PentGo 描述任务或输入 /help`，并保留 `activeStyle` 与 `mutedStyle`：

```go
input.Prompt = "› "
input.Placeholder = "向 PentGo 描述任务或输入 /help"
```

4. 以以下实现替换 `mainView`，并新增紧随其后的四个方法。`contentWidth()` 保持 `max(12, model.width-2)` 不变；渲染内容宽度始终传 `width`。

```go
func (model *terminalModel) mainView() string {
	width := model.contentWidth()
	return lipgloss.JoinVertical(
		lipgloss.Left,
		model.renderHeader(),
		model.viewport.View(),
		model.renderComposer(),
	)
}

func (model *terminalModel) renderHeader() string {
	project := ansi.Truncate(model.projectTitle(), max(8, model.contentWidth()-2), "...")
	status := ansi.Truncate(model.sessionStatus(), max(8, model.contentWidth()-2), "...")
	return headerStyle.Width(model.contentWidth()).Render(lipgloss.JoinVertical(
		lipgloss.Left,
		brandStyle.Render(project),
		metaStyle.Render(status),
	))
}

func (model *terminalModel) renderComposer() string {
	width := model.contentWidth()
	hint := ansi.Truncate("Enter 发送 · Ctrl+O 详情 · Ctrl+C 退出", max(8, width-2), "...")
	body := lipgloss.JoinVertical(lipgloss.Left, model.input.View(), composerHintStyle.Render(hint))
	return inputStyle.Width(width).Render(body)
}

func (model *terminalModel) headerHeight() int {
	return lipgloss.Height(model.renderHeader())
}

func (model *terminalModel) composerHeight() int {
	return lipgloss.Height(model.renderComposer())
}
```

5. 以以下实现替换 `layout`；composer 的边框和说明行不再假定为一行，viewport 永远保持至少 3 行：

```go
func (model *terminalModel) layout() {
	contentWidth := model.contentWidth()
	model.input.Width = max(12, contentWidth-4)
	model.viewport.Width = max(12, contentWidth)
	viewportHeight := model.height - model.headerHeight() - model.composerHeight()
	model.viewport.Height = max(3, viewportHeight)
}
```

6. 在 `renderConversation` 的空内容分支暂时改为下面调用（Task 2 会实现该方法）：

```go
lines = append(lines, model.renderWelcome())
```

- [x] **Step 4: 实现欢迎卡纯渲染函数**

在 `renderConversation` 下方加入。它不读取终端宽度以外的环境状态，project 不存在时安全回退为 `PentGo`：

```go
func (model *terminalModel) renderWelcome() string {
	width := max(12, model.contentWidth()-4)
	project := ansi.Truncate(model.projectTitle(), width, "...")
	content := strings.Join([]string{
		modelStyle.Render("准备开始"),
		"PentGo 会在当前项目中协助你分析目标、调用已配置工具并保留证据。",
		mutedStyle.Render("/load_skill 加载技能  ·  /new 新建会话  ·  /help 查看命令"),
	}, "\n\n")
	return welcomeStyle.Width(width).Render(project + "\n\n" + content)
}
```

- [x] **Step 5: 运行 Task 1 测试并执行格式化**

Run:

```bash
gofmt -w internal/cli/model.go internal/cli/model_test.go
go test ./internal/cli -run 'TestTerminalModelShowsWelcomeCardForEmptySession|TestTerminalModelLayoutKeepsViewportPositiveInNarrowWindow' -count=1
```

Expected: PASS.

- [ ] **Step 6: 提交 Task 1**

```bash
git add internal/cli/model.go internal/cli/model_test.go
git commit -m "feat(cli): add Claude-style terminal shell"
```

### Task 2: 实现折叠工具摘要与确定性详情渲染

**Files:**
- Modify: `internal/cli/model.go:216-232, 445-499`
- Test: `internal/cli/model_test.go`

**Interfaces:**
- Consumes: `agent.Message`、`agent.ToolCall`、Task 1 的 `terminalModel.showToolDetails` 和已存在的 `ansi.Hardwrap`。
- Produces: `renderMessage(message agent.Message, width int, showToolDetails bool) string`、`renderToolCalls(calls []agent.ToolCall, width int, showDetails bool) string`、`renderToolResult(message agent.Message, width int, showDetails bool) string`、`toolSummary(content string, width int) string`、`formatToolArguments(call agent.ToolCall) string`。Task 3 用更新后的 `renderMessage` 调用渲染 transcript。

- [x] **Step 1: 将既有角色渲染测试迁移到新签名，并添加工具摘要/详情测试**

把当前 `TestRenderMessageSeparatesTranscriptRoles` 中的每个 `renderMessage(test.message, 80)` 调用改成 `renderMessage(test.message, 80, false)`。

然后在测试文件增加：

```go
func TestRenderMessageCollapsesToolDetailsByDefault(t *testing.T) {
	message := agent.Message{
		Role:          agent.RoleTool,
		ToolName:      "http_probe",
		ToolArguments: map[string]any{"url": "https://example.test", "timeout": 10},
		Content:       "200 OK\nserver: nginx\nbody: long body that should not appear in summary",
	}
	got := ansi.Strip(renderMessage(message, 80, false))
	for _, want := range []string{"TOOL", "http_probe", "200 OK"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %q", want, got)
		}
	}
	for _, absent := range []string{"server: nginx", "\"url\""} {
		if strings.Contains(got, absent) {
			t.Fatalf("summary unexpectedly contains %q: %q", absent, got)
		}
	}
}

func TestRenderMessageShowsToolDetailsWhenEnabled(t *testing.T) {
	message := agent.Message{
		Role:          agent.RoleTool,
		ToolName:      "http_probe",
		ToolArguments: map[string]any{"url": "https://example.test", "timeout": 10},
		Content:       "200 OK\nserver: nginx",
	}
	got := ansi.Strip(renderMessage(message, 80, true))
	for _, want := range []string{"TOOL RESULT", "http_probe", "Arguments:", "\"timeout\": 10", "server: nginx"} {
		if !strings.Contains(got, want) {
			t.Fatalf("details missing %q: %q", want, got)
		}
	}
}

func TestRenderMessageShowsAssistantToolCallArgumentsOnlyInDetails(t *testing.T) {
	message := agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{
		Name: "http_probe", Arguments: map[string]any{"url": "https://example.test"},
	}}}
	compact := ansi.Strip(renderMessage(message, 80, false))
	detailed := ansi.Strip(renderMessage(message, 80, true))
	if !strings.Contains(compact, "TOOL CALL") || strings.Contains(compact, "\"url\"") {
		t.Fatalf("compact = %q", compact)
	}
	if !strings.Contains(detailed, "Arguments:") || !strings.Contains(detailed, "\"url\": \"https://example.test\"") {
		t.Fatalf("detailed = %q", detailed)
	}
}
```

Also update `TestRenderMessageBoundsToolOutput` to call `renderMessage(..., 80, true)`, because truncation is a detail-mode contract.

- [x] **Step 2: 运行工具渲染测试，确认它们失败**

Run:

```bash
go test ./internal/cli -run 'TestRenderMessage(SeparatesTranscriptRoles|BoundsToolOutput|CollapsesToolDetailsByDefault|ShowsToolDetailsWhenEnabled|ShowsAssistantToolCallArgumentsOnlyInDetails)' -count=1
```

Expected: FAIL，因为 `renderMessage` 尚无第三个参数，也没有摘要和参数格式化实现。

- [x] **Step 3: 让 conversation 传入详情开关**

在 `renderConversation` 中，将：

```go
lines = append(lines, renderMessage(message, model.contentWidth()))
```

替换成：

```go
lines = append(lines, renderMessage(message, model.contentWidth(), model.showToolDetails))
```

- [x] **Step 4: 替换 transcript 渲染函数并增加工具辅助函数**

以以下完整实现替换 `renderMessage` 至 `renderTranscriptBlock` 之前的区域。保留现有的 `renderTranscriptBlock` 和 `indentLines`，让详情工具结果继续最多显示 8 行以保持原来的安全阅读边界。

```go
func renderMessage(message agent.Message, width int, showToolDetails bool) string {
	bodyWidth := max(10, width-2)
	switch message.Role {
	case agent.RoleUser:
		return renderTranscriptBlock(userStyle.Render("YOU"), message.Content, bodyWidth, 0, userBodyStyle)
	case agent.RoleAssistant:
		content := strings.TrimSpace(message.Content)
		parts := make([]string, 0, 2)
		if len(message.ToolCalls) != 0 {
			parts = append(parts, renderToolCalls(message.ToolCalls, bodyWidth, showToolDetails))
		}
		if content != "" {
			parts = append(parts, renderTranscriptBlock(modelStyle.Render("PENTGO"), content, bodyWidth, 0, modelBodyStyle))
		}
		if len(parts) == 0 {
			return mutedStyle.Render("PENTGO  ...")
		}
		return strings.Join(parts, "\n")
	case agent.RoleTool:
		return renderToolResult(message, bodyWidth, showToolDetails)
	}
	return mutedStyle.Render(strings.TrimSpace(message.Content))
}

func renderToolCalls(calls []agent.ToolCall, width int, showDetails bool) string {
	lines := make([]string, 0, len(calls)*2)
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "工具"
		}
		lines = append(lines, toolStyle.Render("TOOL CALL")+"  "+toolBodyStyle.Render(name))
		if showDetails {
			lines = append(lines, renderTranscriptBlock(mutedStyle.Render("Arguments:"), formatToolArguments(call), width, 8, toolBodyStyle))
		}
	}
	return strings.Join(lines, "\n")
}

func renderToolResult(message agent.Message, width int, showDetails bool) string {
	name := strings.TrimSpace(message.ToolName)
	if name == "" {
		name = "工具"
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = "..."
	}
	if !showDetails {
		return toolStyle.Render("TOOL") + "  " + toolBodyStyle.Render(name+" · "+toolSummary(content, width))
	}
	parts := []string{toolStyle.Render("TOOL RESULT"), toolBodyStyle.Render(name)}
	if len(message.ToolArguments) != 0 {
		parts = append(parts, renderTranscriptBlock(mutedStyle.Render("Arguments:"), formatToolArguments(agent.ToolCall{Arguments: message.ToolArguments}), width, 8, toolBodyStyle))
	}
	parts = append(parts, renderTranscriptBlock(mutedStyle.Render("Output:"), content, width, 8, toolBodyStyle))
	return strings.Join(parts, "\n")
}

func toolSummary(content string, width int) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	if firstLine == "" {
		firstLine = "..."
	}
	return ansi.Truncate(firstLine, max(10, width-16), "...")
}

func formatToolArguments(call agent.ToolCall) string {
	if strings.TrimSpace(call.RawArguments) != "" {
		return strings.TrimSpace(call.RawArguments)
	}
	if len(call.Arguments) == 0 {
		return "{}"
	}
	encoded, err := json.MarshalIndent(call.Arguments, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", call.Arguments)
	}
	return string(encoded)
}
```

Add `encoding/json` to the import block. The function deliberately does not infer `ToolArguments` from unrelated assistant calls: provider transcript semantics may contain multiple calls, while tool results already carry their own argument map when available.

- [x] **Step 5: 运行 Task 2 测试**

Run:

```bash
gofmt -w internal/cli/model.go internal/cli/model_test.go
go test ./internal/cli -run 'TestRenderMessage(SeparatesTranscriptRoles|BoundsToolOutput|CollapsesToolDetailsByDefault|ShowsToolDetailsWhenEnabled|ShowsAssistantToolCallArgumentsOnlyInDetails)' -count=1
```

Expected: PASS.

- [ ] **Step 6: 提交 Task 2**

```bash
git add internal/cli/model.go internal/cli/model_test.go
git commit -m "feat(cli): collapse tool output by default"
```

### Task 3: 接入 Ctrl+O 与运行时工具状态块

**Files:**
- Modify: `internal/cli/model.go:105-148, 216-232, 423-443`
- Test: `internal/cli/model_test.go`

**Interfaces:**
- Consumes: Task 1 的 `showToolDetails`、Task 2 的 `renderMessage(message, width, showToolDetails)`；既有 `app.Event` 常量 `EventToolStarted`、`EventToolFinished`、`EventTurnFinished`、`EventTurnFailed`。
- Produces: `terminalModel.toggleToolDetails()` 和扩展后的 `recordEvent(event app.Event)`；两者只更新临时 CLI 状态，绝不写入 Coordinator 或 transcript。

- [x] **Step 1: 添加 Ctrl+O 和 transient 工具状态的失败测试**

在 `internal/cli/model_test.go` 添加：

```go
func TestTerminalModelCtrlOTogglesToolDetails(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	if model.showToolDetails {
		t.Fatal("details should default to collapsed")
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(*terminalModel)
	if !model.showToolDetails {
		t.Fatal("Ctrl+O should show details")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(*terminalModel)
	if model.showToolDetails {
		t.Fatal("second Ctrl+O should collapse details")
	}
}

func TestTerminalModelRendersTransientToolProgress(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventToolStarted, Message: "http_probe"})
	if len(model.activity) != 1 {
		t.Fatalf("activity = %#v", model.activity)
	}
	got := ansi.Strip(model.activity[0])
	for _, want := range []string{"TOOL", "http_probe", "running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("activity missing %q: %q", want, got)
		}
	}

	model.recordEvent(app.Event{Kind: app.EventToolFinished, Message: "http_probe", Output: "200 OK\nignored"})
	got = ansi.Strip(model.activity[len(model.activity)-1])
	for _, want := range []string{"TOOL", "http_probe", "200 OK"} {
		if !strings.Contains(got, want) {
			t.Fatalf("completed activity missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "ignored") {
		t.Fatalf("completed activity should be compact: %q", got)
	}
}
```

- [x] **Step 2: 运行测试，确认 Ctrl+O 和完成事件尚未通过**

Run:

```bash
go test ./internal/cli -run 'TestTerminalModel(CtrlOTogglesToolDetails|RendersTransientToolProgress)' -count=1
```

Expected: FAIL，因为 `tea.KeyCtrlO` 尚未处理，`recordEvent` 还忽略 `EventToolFinished`。

- [x] **Step 3: 在键盘分发中增加 Ctrl+O 并实现开关**

在 `Update` 的 `case tea.KeyMsg` 内、`ctrl+c`/`ctrl+d` 分支之前加入：

```go
case "ctrl+o":
	model.toggleToolDetails()
	return model, nil
```

在 `submit` 函数前添加：

```go
func (model *terminalModel) toggleToolDetails() {
	model.showToolDetails = !model.showToolDetails
	model.renderConversation()
	model.viewport.GotoBottom()
}
```

这个调用不会读取或修改持久化数据；重新渲染使用 Task 2 的已更新 transcript 渲染签名。

- [x] **Step 4: 以统一状态块语言扩展 `recordEvent`**

用以下实现替换现有 `recordEvent`。事件可丢失、最终 transcript 会覆盖临时活动，因此保留已有 `activity` 上限，而不引入新的状态存储。

```go
func (model *terminalModel) recordEvent(event app.Event) {
	name := strings.TrimSpace(event.Message)
	switch event.Kind {
	case app.EventToolStarted:
		if name == "" {
			name = "工具"
		}
		model.addActivity(toolStyle.Render("TOOL") + "  " + toolBodyStyle.Render(name+" · running"))
	case app.EventToolFinished:
		if name == "" {
			name = "工具"
		}
		model.addActivity(toolStyle.Render("TOOL") + "  " + toolBodyStyle.Render(name+" · "+toolSummary(event.Output, model.contentWidth())))
	case app.EventTurnFailed:
		model.addActivity(errorStyle.Render("ERROR  " + name))
	case app.EventTurnFinished:
		if name == "turn interrupted" || name == "runtime stopped" {
			model.addActivity(mutedStyle.Render("STATUS  " + name))
		}
	}
}
```

- [x] **Step 5: 运行 Task 3 测试并确认既有命令输入测试仍通过**

Run:

```bash
gofmt -w internal/cli/model.go internal/cli/model_test.go
go test ./internal/cli -run 'TestTerminalModel(ArrowKeysKeepCurrentSession|CtrlOTogglesToolDetails|RendersTransientToolProgress|NewCommandCreatesBlankSession|NewCommandRejectsArguments)' -count=1
```

Expected: PASS.

- [ ] **Step 6: 提交 Task 3**

```bash
git add internal/cli/model.go internal/cli/model_test.go
git commit -m "feat(cli): toggle tool details with Ctrl+O"
```

### Task 4: 全量回归、视觉语义审查和完成文档

**Files:**
- Modify: `internal/cli/model.go`（仅在以下验证发现真实缺陷时）
- Modify: `internal/cli/model_test.go`（仅在以下验证发现遗漏的确定性回归时）
- Modify: `docs/superpowers/plans/2026-08-20-claude-code-tui-refresh.md`（勾选完成步骤）

**Interfaces:**
- Consumes: Tasks 1–3 的全部 CLI 行为。
- Produces: 已验证、格式化的 TUI 改造；没有新的程序接口。

- [x] **Step 1: 增加端到端视图语义回归测试**

在 `internal/cli/model_test.go` 增加下面测试，确保有 transcript 时欢迎态消失，且紧凑工具语义能通过 model 的真实渲染路径到达用户：

```go
func TestTerminalModelTranscriptReplacesWelcomeAndUsesCompactTools(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	transcript := coordinator.Messages(session.ID)
	if len(transcript) != 0 {
		t.Fatalf("new transcript = %#v", transcript)
	}
	model := newTerminalModel(context.Background(), coordinator, session.ID)
	model.width, model.height = 100, 32
	model.activity = []string{toolStyle.Render("TOOL") + "  " + toolBodyStyle.Render("http_probe · 200 OK")}
	model.refresh()

	view := ansi.Strip(model.View())
	if strings.Contains(view, "准备开始") {
		t.Fatalf("welcome must disappear after activity: %q", view)
	}
	for _, want := range []string{"TOOL", "http_probe", "200 OK"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
}
```

- [x] **Step 2: 运行 CLI 包全部测试**

Run:

```bash
gofmt -w internal/cli/model.go internal/cli/model_test.go
go test ./internal/cli -count=1
```

Expected: PASS.

- [x] **Step 3: 运行项目质量门禁**

Run:

```bash
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
```

Expected: every command exits `0`; `git diff --check` prints no whitespace errors.

- [x] **Step 4: 审查规格覆盖范围并手动试运行**

Run:

```bash
go run ./cmd/pentgo
```

Expected: 在可交互终端中看到紧凑标题栏、欢迎卡和带 `Enter 发送 · Ctrl+O 详情 · Ctrl+C 退出` 的 composer。输入 `/status` 验证欢迎卡消失；在具备可调用工具的配置中提交一个会触发工具的任务，验证默认摘要和 `Ctrl+O` 详情切换。按 `Ctrl+C` 退出。

Review against `docs/superpowers/specs/2026-08-20-claude-code-tui-refresh.md`:

- 单列、单会话和现有命令是否仍在；
- 空态、标题、composer、所有消息角色和错误是否有明确文本标签；
- 详情关闭时是否没有参数 JSON 或多行工具输出，开启时是否可以检查参数和受限完整输出；
- 80/100/窄尺寸下是否无明显截断错位或遮挡。

若发现确定性缺陷，在 `internal/cli/model_test.go` 先写一个最小回归测试，修复 `internal/cli/model.go`，然后重复 Step 2 和 Step 3；不将不相关现有工作树改动纳入本功能。

- [ ] **Step 5: 更新计划勾选状态并提交最终验证**

将本文件所有完成步骤改为 `- [x]`，然后：

```bash
git add internal/cli/model.go internal/cli/model_test.go docs/superpowers/plans/2026-08-20-claude-code-tui-refresh.md
git commit -m "test(cli): verify Claude-style TUI refresh"
```

Expected: commit 只包含本功能的 CLI 源码、测试和计划文档；若仓库已有其他未提交变更，使用路径限定的 `git add`，绝不使用 `git add -A`。


## Execution Record

- Completed Tasks 1–4 through the tested implementation and final validation.
- Review findings on activity isolation, duplicate tool events, terminal-control sanitization, and ready/running state were fixed before final verification.
- The per-task and final commit steps remain intentionally unchecked: `internal/cli/model.go` and `internal/cli/model_test.go` were already untracked before this implementation began, so their full-file commit requires the owner's explicit review.

## Self-Review

### Spec coverage

| 规格要求 | 覆盖任务 |
| --- | --- |
| 克制色彩、细边框、输入 panel 和窄终端适配 | Task 1 |
| 上下文标题、欢迎卡与快捷键提示 | Task 1 |
| 用户、助手、工具、结果和错误的角色层级 | Tasks 1–3 |
| 默认紧凑工具摘要与 `Ctrl+O` 全局详情 | Tasks 2–3 |
| 工具进行中/完成/失败的临时事件呈现 | Task 3 |
| 不改业务层、持久化或会话架构 | Global Constraints，所有任务仅修改 CLI |
| 单元、整包、全仓库、构建、静态检查和手动验证 | Tasks 1–4 |

No approved-spec requirement is uncovered.

### Placeholder scan

This plan contains no `TBD`、`TODO`、`implement later`、`fill in details`、`similar to Task` 或“write tests for the above”式占位步骤。每个代码修改均给出目标路径、签名和完整代码块；Task 4 的“若发现缺陷”限定为验证中发现的具体回归，不是未定义的功能范围。

### Type consistency

- `terminalModel.showToolDetails bool` 在 Task 1 定义，在 Tasks 2–3 消费。
- `renderMessage(message agent.Message, width int, showToolDetails bool) string` 在 Task 2 定义，并在 Task 2 的 `renderConversation` 与 Task 2/既有测试中一致调用。
- `toolSummary(content string, width int) string` 在 Task 2 定义，并在 Task 2 的工具结果与 Task 3 的 `EventToolFinished` 中一致调用。
- `headerHeight() int` 与 `composerHeight() int` 在 Task 1 定义并只用于 Task 1 `layout` 与其测试。
- `tea.KeyCtrlO` 的键盘消息转换为 `typed.String() == "ctrl+o"`，与 Bubble Tea 既有 `typed.String()` 分发方式一致。
