package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"pentgo/internal/agent"
	"pentgo/internal/app"
	"pentgo/internal/domain"
)

// 默认终端尺寸。
const (
	defaultWidth  = 100
	defaultHeight = 32
)

// 共享的 Lip Gloss 样式使视图渲染与终端状态分离。
var (
	shellStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	brandStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	metaStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("252")).
			Padding(0, 1)
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	composerHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	welcomeStyle      = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Foreground(lipgloss.Color("252")).
				Padding(1, 2)
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	activeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("151")).Bold(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
	userBodyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	modelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	modelBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true)
	toolBodyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
)

// activityLevel 定义临时 CLI 反馈的语义层级；文本始终以安全纯文本保存。
type activityLevel uint8

const (
	activityInfo activityLevel = iota
	activityError
	activityStatus
)

// activityEntry 是不进入 transcript 的命令反馈或运行时错误。
type activityEntry struct {
	level activityLevel
	text  string
}

// terminalModel 是 Bubble Tea 的临时视图模型。它通过 Coordinator 读取持久化状态，
// 仅保留焦点、视口、运行中的工具和最近的 UI 活动。
type terminalModel struct {
	ctx             context.Context
	coordinator     *app.Coordinator
	input           textinput.Model
	viewport        viewport.Model
	project         *domain.Project
	sessions        []*domain.Session
	focused         string
	eventCancel     context.CancelFunc
	activity        []activityEntry
	streamActivity  []activityEntry
	runningTools    map[string]int
	turnRunning     bool
	generating      bool
	showToolDetails bool
	width           int
	height          int
	err             error
}

// runtimeEventMsg 将一个 worker 事件转发到 Bubble Tea 更新循环。
type runtimeEventMsg struct {
	sessionID string
	event     app.Event
	ok        bool
}

// turnCompleteMsg 表示 TUI 提交的异步命令已完成。
type turnCompleteMsg struct {
	sessionID string
	err       error
}

// contextDoneMsg 在运行时根上下文结束时通知 Bubble Tea 退出。
type contextDoneMsg struct{ err error }

// newTerminalModel 初始化输入框、最小视口和已聚焦会话。
func newTerminalModel(ctx context.Context, coordinator *app.Coordinator, focused string) *terminalModel {
	if ctx == nil {
		ctx = context.Background()
	}
	input := textinput.New()
	input.Prompt = "› "
	input.Placeholder = "向 PentGo 描述任务或输入 /help"
	input.CharLimit = 16 * 1024
	input.PromptStyle = activeStyle
	input.PlaceholderStyle = mutedStyle
	model := &terminalModel{
		ctx:          ctx,
		coordinator:  coordinator,
		input:        input,
		viewport:     viewport.New(1, 1),
		focused:      focused,
		runningTools: make(map[string]int),
		width:        defaultWidth,
		height:       defaultHeight,
	}
	model.layout()
	model.refresh()
	if coordinator != nil {
		for _, diagnostic := range coordinator.SkillDiagnostics() {
			model.addActivity(activityError, fmt.Sprintf("技能已跳过：%s：%s", diagnostic.Path, diagnostic.Reason))
		}
		model.refresh()
	}
	return model
}

// Init 聚焦输入框，并启动当前会话和上下文监听。
func (model *terminalModel) Init() tea.Cmd {
	return tea.Batch(model.input.Focus(), model.watchSession(model.focused), model.waitContext())
}

// Update 将终端输入和 worker 事件转换为模型状态变更。
// 耗时操作始终留在 Tea 命令或应用运行时中执行。
func (model *terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = typed.Width, typed.Height
		model.layout()
		model.renderConversation()
		return model, nil
	case contextDoneMsg:
		model.err = typed.err
		return model, tea.Quit
	case runtimeEventMsg:
		if typed.ok && typed.sessionID == model.focused {
			model.recordEvent(typed.event)
			model.refresh()
			return model, model.watchSession(model.focused)
		}
		return model, nil
	case turnCompleteMsg:
		// 即使有界进度事件被丢弃，完成消息也必须收敛当前会话的临时生成状态。
		if typed.sessionID == model.focused {
			model.turnRunning = false
			model.generating = false
			model.runningTools = make(map[string]int)
		}
		if typed.err != nil && !model.hasActivity(activityError, typed.err.Error()) {
			model.addActivity(activityError, typed.err.Error())
		}
		model.refresh()
		return model, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "ctrl+o":
			model.toggleToolDetails()
			return model, nil
		case "ctrl+c", "ctrl+d":
			return model, tea.Quit
		case "enter", "ctrl+j":
			line := strings.TrimSpace(model.input.Value())
			if line == "" {
				return model, nil
			}
			model.input.Reset()
			return model, model.handleLine(line)
		}
	}

	var inputCommand tea.Cmd
	model.input, inputCommand = model.input.Update(message)
	var viewportCommand tea.Cmd
	model.viewport, viewportCommand = model.viewport.Update(message)
	return model, tea.Batch(inputCommand, viewportCommand)
}

// View 渲染全宽的单列对话界面。
func (model *terminalModel) View() string {
	if model.width <= 0 || model.height <= 0 {
		return "PentGo"
	}
	return model.mainView()
}

// mainView 组合紧凑上下文栏、transcript 视口和带快捷键提示的 composer。
func (model *terminalModel) mainView() string {
	return shellStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		model.renderHeader(),
		model.viewport.View(),
		model.renderComposer(),
	))
}

// renderHeader 将项目和当前会话上下文收敛到一个紧凑的 Claude Code 风格标题栏。
func (model *terminalModel) renderHeader() string {
	width := model.contentWidth()
	project := ansi.Truncate(model.projectTitle(), max(8, width-2), "...")
	status := ansi.Truncate(model.sessionStatus(), max(8, width-2), "...")
	return headerStyle.Width(width).Render(lipgloss.JoinVertical(
		lipgloss.Left,
		brandStyle.Render(project),
		metaStyle.Render(status),
	))
}

// renderComposer 包含输入和固定可发现的快捷键提示。
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

// projectTitle 在 Coordinator 状态尚不可用时平稳渲染标题。
func (model *terminalModel) projectTitle() string {
	if model.project == nil {
		return "PentGo"
	}
	return "PentGo  " + safeTerminalText(model.project.Name)
}

// sessionStatus 返回当前聚焦会话的紧凑描述，并明确展示 ready/running 状态。
func (model *terminalModel) sessionStatus() string {
	for _, session := range model.sessions {
		if session.ID == model.focused {
			name := safeTerminalText(session.Name)
			if name == "" {
				name = safeTerminalText(session.ID)
			}
			status := "ready"
			if model.turnRunning || (session.ActiveTurn != nil && session.ActiveTurn.Status == domain.TurnRunning) {
				status = "running"
			}
			return fmt.Sprintf("会话  %s  |  %s  |  %d 轮  |  %s", name, safeTerminalText(session.ID), session.Turns, status)
		}
	}
	return "会话  未选择"
}

// layout 在窗口调整后为标题栏和 composer 预留空间，并保证视口可读。
func (model *terminalModel) layout() {
	contentWidth := model.contentWidth()
	// textinput.View adds the prompt and one cursor cell to Width for non-empty input.
	// Reserve the composer border/padding plus those two prompt cells to keep the
	// rendered input line inside the rounded box for CJK, emoji, and long values.
	model.input.Width = max(12, contentWidth-5)
	model.viewport.Width = max(12, contentWidth)
	viewportHeight := model.height - model.headerHeight() - model.composerHeight()
	model.viewport.Height = max(3, viewportHeight)
}

// contentWidth 为全宽对话布局预留边界空间。
func (model *terminalModel) contentWidth() int {
	return max(12, model.width-2)
}

// refresh 从运行时获取当前快照并重新渲染视口。
func (model *terminalModel) refresh() {
	if model.coordinator == nil {
		return
	}
	model.project, _ = model.coordinator.CurrentProject()
	model.sessions = model.coordinator.Sessions()
	model.renderConversation()
}

// renderConversation 将持久化 transcript、仅在运行期间显示的工具状态和会话内 UI 活动组合为对话内容。
func (model *terminalModel) renderConversation() {
	if model.coordinator == nil {
		return
	}
	lines := make([]string, 0)
	for _, message := range model.coordinator.Messages(model.focused) {
		if message.Role == agent.RoleSystem {
			continue
		}
		lines = append(lines, renderMessage(message, model.contentWidth(), model.showToolDetails, model.runningTools))
	}
	if model.generating {
		lines = append(lines, renderGeneratingPlaceholder())
	}
	if len(lines) == 0 && len(model.activity) == 0 && len(model.streamActivity) == 0 {
		lines = append(lines, model.renderWelcome())
	}
	for _, activity := range model.streamActivity {
		lines = append(lines, ansi.Hardwrap(renderActivity(activity), model.contentWidth(), true))
	}
	for _, activity := range model.activity {
		lines = append(lines, ansi.Hardwrap(renderActivity(activity), model.contentWidth(), true))
	}
	model.viewport.SetContent(strings.Join(lines, "\n\n"))
	model.viewport.GotoBottom()
}

// renderGeneratingPlaceholder makes a pending non-streaming turn visible without creating transcript content.
func renderGeneratingPlaceholder() string {
	return modelStyle.Render("PENTGO · 正在生成…")
}

// renderActivity applies presentation only after the entry text has been sanitized and stored.
func renderActivity(entry activityEntry) string {
	switch entry.level {
	case activityError:
		return errorStyle.Render("ERROR  " + entry.text)
	case activityStatus:
		return mutedStyle.Render("STATUS  " + entry.text)
	default:
		return mutedStyle.Render(entry.text)
	}
}

// renderWelcome 为还没有 transcript 或临时活动的新会话提供可发现的起点。
func (model *terminalModel) renderWelcome() string {
	width := max(12, model.contentWidth()-4)
	project := ansi.Truncate(model.projectTitle(), width, "...")
	content := strings.Join([]string{
		modelStyle.Render("准备开始"),
		"PentGo 会在当前项目中协助你分析目标、调用已配置工具并保留证据。",
		mutedStyle.Render("/new 新建会话  ·  /help 查看命令"),
	}, "\n\n")
	return welcomeStyle.Width(width).Render(project + "\n\n" + content)
}

// watchSession 在焦点变化时替换此前的事件订阅。
// 子上下文会阻止此前聚焦会话的过期事件更新 UI。
func (model *terminalModel) watchSession(sessionID string) tea.Cmd {
	if model.eventCancel != nil {
		model.eventCancel()
		model.eventCancel = nil
	}
	if sessionID == "" {
		return nil
	}
	events := model.coordinator.Events(sessionID)
	if events == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(model.ctx)
	model.eventCancel = cancel
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			return runtimeEventMsg{sessionID: sessionID, event: event, ok: ok}
		}
	}
}

// waitContext 将根上下文取消转换为 Bubble Tea 退出消息。
func (model *terminalModel) waitContext() tea.Cmd {
	return func() tea.Msg {
		<-model.ctx.Done()
		return contextDoneMsg{err: model.ctx.Err()}
	}
}

// handleLine 将用户文本派发至聚焦会话，或解释斜杠命令。
func (model *terminalModel) handleLine(line string) tea.Cmd {
	command, argument, _ := strings.Cut(line, " ")
	argument = strings.TrimSpace(argument)
	if !strings.HasPrefix(command, "/") {
		if model.focused == "" {
			return model.startSessionWithMessage(line)
		}
		return model.submit(model.focused, line)
	}
	switch command {
	case "/quit", "/exit":
		return tea.Quit
	case "/status":
		model.addActivity(activityInfo, model.sessionStatus())
	case "/facts":
		model.addActivity(activityInfo, model.coordinator.FactIndex())
	case "/session":
		return model.handleSessionCommand(argument)
	case "/new":
		if argument != "" {
			model.addActivity(activityError, "错误：/new 不接受参数")
			model.renderConversation()
			return nil
		}
		return model.startSession()
	case "/clear":
		model.clearTransientState()
		model.focused = ""
		model.watchSession("")
	case "/help":
		model.addActivity(activityInfo, "/new  /session rename|list|delete  /status  /facts  /clear  /exit")
	case "/project":
		verb, value, _ := strings.Cut(argument, " ")
		if verb != "new" || strings.TrimSpace(value) == "" {
			model.addActivity(activityError, "错误：项目命令为 /project new NAME")
			return nil
		}
		project, err := model.coordinator.CreateProject(model.ctx, strings.TrimSpace(value))
		if err != nil {
			model.addActivity(activityError, "错误："+err.Error())
		} else {
			model.addActivity(activityInfo, "项目已打开："+project.ID)
		}
	default:
		model.addActivity(activityError, "错误：未知命令")
	}
	model.refresh()
	return nil
}

// handleSessionCommand 处理需要可选参数的会话操作。
func (model *terminalModel) handleSessionCommand(argument string) tea.Cmd {
	verb, value, _ := strings.Cut(argument, " ")
	value = strings.TrimSpace(value)
	switch verb {
	case "rename":
		if model.focused == "" {
			model.addActivity(activityError, "错误：没有聚焦的会话")
			return nil
		}
		if value == "" {
			model.addActivity(activityError, "错误：会话名称为空")
			return nil
		}
		if err := model.coordinator.RenameSession(model.focused, value); err != nil {
			model.addActivity(activityError, "错误："+err.Error())
		} else {
			model.addActivity(activityInfo, "会话已重命名："+value)
		}
	case "list":
		for _, session := range model.coordinator.Sessions() {
			model.addActivity(activityInfo, fmt.Sprintf("%s turns=%d", session.ID, session.Turns))
		}
	case "delete":
		if value == "" {
			value = model.focused
		}
		if value == "" {
			model.addActivity(activityError, "错误：没有可删除的会话")
			return nil
		}
		wasFocused := value == model.focused
		if err := model.coordinator.DeleteSession(value); err != nil {
			model.addActivity(activityError, "错误："+err.Error())
		} else if wasFocused {
			model.focused = ""
			model.clearTransientState()
			model.watchSession("")
		} else {
			model.addActivity(activityInfo, "会话已删除："+value)
		}
	default:
		model.addActivity(activityError, "错误：会话命令为 rename、list 或 delete")
	}
	model.refresh()
	return nil
}

// startSession 创建并聚焦一个空的交互会话。
func (model *terminalModel) startSession() tea.Cmd {
	session, err := model.coordinator.NewSession("新会话")
	if err != nil {
		model.addActivity(activityError, "错误："+err.Error())
		return nil
	}
	model.focused = session.ID
	model.clearTransientState()
	model.refresh()
	return model.watchSession(session.ID)
}

// startSessionWithMessage 创建会话后立即提交首条输入。
func (model *terminalModel) startSessionWithMessage(message string) tea.Cmd {
	session, err := model.coordinator.NewSession(message)
	if err != nil {
		model.addActivity(activityError, "错误："+err.Error())
		return nil
	}
	model.focused = session.ID
	model.clearTransientState()
	model.refresh()
	return tea.Batch(model.watchSession(session.ID), model.submit(session.ID, message))
}

// focusedSession 在不再次查询运行时的情况下查找当前会话快照。
func (model *terminalModel) focusedSession() *domain.Session {
	for _, session := range model.sessions {
		if session.ID == model.focused {
			return session
		}
	}
	return nil
}

// toggleToolDetails 仅切换当前 TUI 的工具详情显示，不会变更 transcript 或 evidence。
func (model *terminalModel) toggleToolDetails() {
	model.showToolDetails = !model.showToolDetails
	model.renderConversation()
	model.viewport.GotoBottom()
}

// submit 异步等待 worker 结果，以保持 Bubble Tea 响应流畅。
func (model *terminalModel) submit(sessionID, message string) tea.Cmd {
	return func() tea.Msg {
		done := model.coordinator.Submit(model.ctx, sessionID, message)
		return turnCompleteMsg{sessionID: sessionID, err: <-done}
	}
}

// recordEvent 只追踪尚未结束的工具和非 transcript 的失败/中断状态，避免与已持久化消息重复。
func (model *terminalModel) recordEvent(event app.Event) {
	name := safeTerminalText(event.Message)
	switch event.Kind {
	case app.EventTurnStarted:
		model.turnRunning = true
		model.generating = true
	case app.EventAssistantDelta:
		model.generating = true
		if name != "" {
			model.addStreamActivity(name)
		}
	case app.EventContextActivity:
		level := activityInfo
		if activity, ok := event.Data.(agent.ContextActivity); ok {
			switch activity.Kind {
			case agent.ContextRequestRejected:
				level = activityError
			case agent.ContextCheckpointCreated, agent.ContextToolPruned, agent.ContextOverflowRetry:
				level = activityStatus
			}
		}
		model.addActivity(level, name)
	case app.EventAssistantMessage:
		model.generating = false
		model.streamActivity = nil
	case app.EventToolStarted:
		model.generating = false
		model.streamActivity = nil
		if name == "" {
			name = "工具"
		}
		model.runningTools[name]++
	case app.EventToolFinished:
		if name == "" {
			name = "工具"
		}
		model.finishRunningTool(name)
		if model.turnRunning && len(model.runningTools) == 0 {
			model.generating = true
		}
	case app.EventTurnFailed:
		model.turnRunning = false
		model.generating = false
		model.runningTools = make(map[string]int)
		model.addActivity(activityError, name)
	case app.EventTurnFinished:
		model.turnRunning = false
		model.generating = false
		model.runningTools = make(map[string]int)
		if name == "turn interrupted" || name == "runtime stopped" {
			model.addActivity(activityStatus, name)
		}
	}
}

func (model *terminalModel) finishRunningTool(name string) {
	if model.runningTools[name] <= 1 {
		delete(model.runningTools, name)
		return
	}
	model.runningTools[name]--
}

// clearTransientState isolates command feedback and live execution state to the current focused session.
func (model *terminalModel) clearTransientState() {
	model.activity = nil
	model.streamActivity = nil
	model.runningTools = make(map[string]int)
	model.turnRunning = false
	model.generating = false
}

// addActivity 独立于持久化 transcript 限制临时 UI 输出的长度，并且只保存安全纯文本。
func (model *terminalModel) addStreamActivity(value string) {
	value = safeTerminalText(value)
	if value == "" {
		return
	}
	model.streamActivity = append(model.streamActivity, activityEntry{level: activityInfo, text: value})
	if len(model.streamActivity) > 4 {
		model.streamActivity = append([]activityEntry(nil), model.streamActivity[len(model.streamActivity)-4:]...)
	}
}

func (model *terminalModel) addActivity(level activityLevel, value string) {
	value = safeTerminalText(value)
	if value == "" {
		return
	}
	model.activity = append(model.activity, activityEntry{level: level, text: value})
	if len(model.activity) > 24 {
		model.activity = append([]activityEntry(nil), model.activity[len(model.activity)-24:]...)
	}
}

func (model *terminalModel) hasActivity(level activityLevel, value string) bool {
	value = safeTerminalText(value)
	for _, entry := range model.activity {
		if entry.level == level && entry.text == value {
			return true
		}
	}
	return false
}

// renderMessage 以角色标题和缩进正文呈现持久化消息，并按详情开关折叠工具内容。
// runningTools 是可选的临时状态，仅由终端模型传入；纯渲染测试可省略它。
func renderMessage(message agent.Message, width int, showToolDetails bool, runningTools ...map[string]int) string {
	bodyWidth := max(10, width-2)
	running := map[string]int(nil)
	if len(runningTools) != 0 {
		running = runningTools[0]
	}
	switch message.Role {
	case agent.RoleUser:
		return renderTranscriptBlock(userStyle.Render("YOU"), message.Content, bodyWidth, 0, userBodyStyle)
	case agent.RoleAssistant:
		content := strings.TrimSpace(safeTerminalText(message.Content))
		parts := make([]string, 0, 2)
		if len(message.ToolCalls) != 0 {
			parts = append(parts, renderToolCalls(message.ToolCalls, bodyWidth, showToolDetails, running))
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
	return mutedStyle.Render(safeTerminalText(message.Content))
}

// renderToolCalls keeps tool planning scannable, marks only currently running calls, and reveals arguments in detail mode.
func renderToolCalls(calls []agent.ToolCall, width int, showDetails bool, runningTools map[string]int) string {
	lines := make([]string, 0, len(calls)*2)
	for _, call := range calls {
		name := safeTerminalText(call.Name)
		if name == "" {
			name = "工具"
		}
		label := name
		if runningTools[name] > 0 {
			label += " · running"
		}
		lines = append(lines, toolStyle.Render("TOOL CALL")+"  "+toolBodyStyle.Render(label))
		if showDetails {
			lines = append(lines, renderTranscriptBlock(mutedStyle.Render("Arguments:"), formatToolArguments(call), width, 8, toolBodyStyle))
		}
	}
	return strings.Join(lines, "\n")
}

// renderToolResult renders a one-line audit summary by default and bounded output in detail mode.
func renderToolResult(message agent.Message, width int, showDetails bool) string {
	name := safeTerminalText(message.ToolName)
	if name == "" {
		name = "工具"
	}
	content := strings.TrimSpace(safeTerminalText(message.Content))
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

// toolSummary extracts one safe line for the default compact tool block.
func toolSummary(content string, width int) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(safeTerminalText(content)), "\n")
	if firstLine == "" {
		firstLine = "..."
	}
	return ansi.Truncate(firstLine, max(10, width-16), "...")
}

// formatToolArguments returns provider raw JSON when present, otherwise stable indented JSON.
func formatToolArguments(call agent.ToolCall) string {
	if strings.TrimSpace(call.RawArguments) != "" {
		return strings.TrimSpace(safeTerminalText(call.RawArguments))
	}
	if len(call.Arguments) == 0 {
		return "{}"
	}
	encoded, err := json.MarshalIndent(call.Arguments, "", "  ")
	if err != nil {
		return safeTerminalText(fmt.Sprintf("%v", call.Arguments))
	}
	return safeTerminalText(string(encoded))
}

// safeTerminalText removes ANSI escape/control characters from untrusted transcript, event and command text.
// Newlines remain for intentional text structure; tabs become spaces so they cannot alter the layout.
func safeTerminalText(value string) string {
	value = ansi.Strip(value)
	var result strings.Builder
	result.Grow(len(value))
	for _, character := range value {
		switch character {
		case '\n':
			result.WriteRune(character)
		case '\t':
			result.WriteString("  ")
		default:
			if !unicode.IsControl(character) {
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

// renderTranscriptBlock 统一正文换行、缩进和工具输出的最大显示行数。
func renderTranscriptBlock(title, content string, width, lineLimit int, bodyStyle lipgloss.Style) string {
	content = strings.TrimSpace(safeTerminalText(content))
	if content == "" {
		content = "..."
	}
	lines := strings.Split(ansi.Hardwrap(content, width, true), "\n")
	if lineLimit > 0 && len(lines) > lineLimit {
		lines = append(lines[:lineLimit], "... 已截断")
	}
	return title + "\n" + bodyStyle.Render(indentLines(strings.Join(lines, "\n"), 2))
}

// indentLines 保持多行正文的左边界对齐，并避免每类消息重复拼接前缀。
func indentLines(value string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	return indent + strings.ReplaceAll(value, "\n", "\n"+indent)
}

// max 返回较大的整数，用于限制布局尺寸。
func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
