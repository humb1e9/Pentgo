package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	projectmodel "pentgo/internal/project"
	sessionstate "pentgo/internal/session"
)

// 默认终端尺寸。
const (
	defaultWidth               = 100
	defaultHeight              = 32
	startupActivityDisplayTime = 3 * time.Second
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
	welcomeStyle = lipgloss.NewStyle().
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
	actionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63")).Bold(true).Padding(0, 1)
	pauseStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
)

// activityLevel 定义临时 CLI 反馈的语义层级；文本始终以安全纯文本保存。
type activityLevel uint8

const (
	activityInfo activityLevel = iota
	activityError
	activityStatus
)

// activityEntry 是不进入 conversation 的命令反馈或运行时错误。
type activityEntry struct {
	level activityLevel
	text  string
}

// terminalModel 是 Bubble Tea 的临时视图模型。它通过 Coordinator 读取持久化状态，
// 仅保留焦点、视口、运行中的工具和最近的 UI 活动。
type terminalModel struct {
	ctx             context.Context
	coordinator     Controller
	input           textinput.Model
	inputHistory    []string
	historyIndex    int
	historyDraft    string
	viewport        viewport.Model
	project         *projectmodel.Project
	sessions        []*sessionstate.Session
	focused         string
	eventCancel     context.CancelFunc
	activity        []activityEntry
	startupActivity []activityEntry
	streamText      string
	runningTools    map[string]int
	turnRunning     bool
	generating      bool
	turnErrorShown  bool
	showToolDetails bool
	width           int
	height          int
	err             error
}

// runtimeEventMsg 将一个 worker 事件转发到 Bubble Tea 更新循环。
type runtimeEventMsg struct {
	sessionID string
	event     sessionstate.Event
	ok        bool
}

// turnCompleteMsg 表示 TUI 提交的异步命令已完成。
type turnCompleteMsg struct {
	sessionID string
	err       error
}

// contextDoneMsg 在运行时根上下文结束时通知 Bubble Tea 退出。
type contextDoneMsg struct{ err error }

// dismissStartupActivityMsg removes one-time startup diagnostics after a brief notice.
type dismissStartupActivityMsg struct{}

// newTerminalModel 初始化输入框、最小视口和已聚焦会话。
func newTerminalModel(ctx context.Context, coordinator Controller, focused string) *terminalModel {
	if ctx == nil {
		ctx = context.Background()
	}
	input := textinput.New()
	input.Prompt = "› "
	input.Placeholder = "向PentGo描述任务"
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
		historyIndex: -1,
	}
	model.layout()
	model.refresh()
	model.loadInputHistory()
	if coordinator != nil {
		for _, diagnostic := range coordinator.SkillDiagnostics() {
			model.startupActivity = append(model.startupActivity, activityEntry{level: activityError, text: fmt.Sprintf("技能已跳过：%s：%s", diagnostic.Path, diagnostic.Reason)})
		}
		model.refresh()
	}
	return model
}

// Init 聚焦输入框，并启动当前会话和上下文监听。
func (model *terminalModel) Init() tea.Cmd {
	commands := []tea.Cmd{model.input.Focus(), model.watchSession(model.focused), model.waitContext()}
	if len(model.startupActivity) != 0 {
		commands = append(commands, dismissStartupActivityAfter(startupActivityDisplayTime))
	}
	return tea.Batch(commands...)
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
	case dismissStartupActivityMsg:
		model.dismissStartupActivity()
		model.refresh()
		return model, nil
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
		if typed.err != nil {
			model.addTurnError(typed.err.Error())
		}
		model.refresh()
		return model, nil
	case tea.MouseMsg:
		if typed.Type == tea.MouseLeft && typed.Action == tea.MouseActionPress {
			if model.actionButtonHit(typed) {
				return model, model.handleActionButton()
			}
			if model.composerHit(typed) {
				return model, model.input.Focus()
			}
			model.input.Blur()
		}
		switch typed.Type {
		case tea.MouseWheelUp:
			if model.composerHit(typed) {
				model.previousInput()
			} else {
				model.input.Blur()
				model.viewport.ScrollUp(model.viewport.MouseWheelDelta)
			}
		case tea.MouseWheelDown:
			if model.composerHit(typed) {
				model.nextInput()
			} else {
				model.input.Blur()
				model.viewport.ScrollDown(model.viewport.MouseWheelDelta)
			}
		}
		return model, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "ctrl+o":
			model.toggleToolDetails()
			return model, nil
		case "up":
			if model.input.Focused() {
				model.previousInput()
			} else {
				model.viewport.LineUp(1)
			}
			return model, nil
		case "down":
			if model.input.Focused() {
				model.nextInput()
			} else {
				model.viewport.LineDown(1)
			}
			return model, nil
		case "pgup":
			if model.input.Focused() {
				model.previousInput()
			} else {
				model.viewport.PageUp()
			}
			return model, nil
		case "pgdown":
			if model.input.Focused() {
				model.nextInput()
			} else {
				model.viewport.PageDown()
			}
			return model, nil
		case "ctrl+u":
			model.viewport.PageUp()
			return model, nil
		case "ctrl+d":
			model.viewport.PageDown()
			return model, nil
		case "home":
			if !model.input.Focused() {
				model.viewport.GotoTop()
				return model, nil
			}
		case "end":
			if !model.input.Focused() {
				model.viewport.GotoBottom()
				return model, nil
			}
		case "ctrl+c":
			return model, tea.Quit
		case "enter", "ctrl+j":
			line := strings.TrimSpace(model.input.Value())
			if line == "" {
				return model, model.handleActionButton()
			}
			model.input.Reset()
			return model, model.handleLine(line)
		}
	}

	if _, ok := message.(tea.KeyMsg); ok {
		model.resetInputNavigation()
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

// mainView 组合紧凑上下文栏、conversation 视口和 composer。
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

// renderComposer puts the clickable send/pause action beside the input.
func (model *terminalModel) renderComposer() string {
	width := model.contentWidth()
	line := lipgloss.JoinHorizontal(lipgloss.Top, model.input.View(), " ", model.renderActionButton())
	return inputStyle.Width(width).Render(line)
}

func (model *terminalModel) renderActionButton() string {
	if model.turnRunning {
		return pauseStyle.Render("■")
	}
	return actionStyle.Render("➜")
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
			if model.turnRunning || (session.ActiveTurn != nil && session.ActiveTurn.Status == sessionstate.TurnRunning) {
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
	// Reserve border/padding, the prompt/cursor cells, gap, and the action button.
	model.input.Width = max(12, contentWidth-lipgloss.Width(model.renderActionButton())-7)
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

// renderConversation 将持久化 conversation、仅在运行期间显示的工具状态和会话内 UI 活动组合为对话内容。
func (model *terminalModel) renderConversation() {
	if model.coordinator == nil {
		return
	}
	lines := make([]string, 0)
	for _, message := range model.coordinator.Messages(model.focused) {
		if message.Role == sessionstate.RoleSystem {
			continue
		}
		lines = append(lines, renderMessage(message, model.contentWidth(), model.showToolDetails, model.runningTools))
	}
	if model.generating {
		lines = append(lines, renderGeneratingPlaceholder())
	}
	if len(lines) == 0 && len(model.activity) == 0 && len(model.startupActivity) == 0 && model.streamText == "" {
		lines = append(lines, model.renderWelcome())
	}
	for _, activity := range model.startupActivity {
		lines = append(lines, ansi.Hardwrap(renderActivity(activity), model.contentWidth(), true))
	}
	if model.streamText != "" {
		lines = append(lines, renderConversationBlock(modelStyle.Render("PENTGO"), model.streamText, model.contentWidth(), 0, modelBodyStyle))
	}
	for _, activity := range model.activity {
		lines = append(lines, ansi.Hardwrap(renderActivity(activity), model.contentWidth(), true))
	}
	model.viewport.SetContent(strings.Join(lines, "\n\n"))
	model.viewport.GotoBottom()
}

// renderGeneratingPlaceholder makes a pending non-streaming turn visible without creating conversation content.
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

// renderWelcome 为还没有 conversation 或临时活动的新会话提供可发现的起点。
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

func dismissStartupActivityAfter(delay time.Duration) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(delay)
		return dismissStartupActivityMsg{}
	}
}

// handleLine 将用户文本派发至聚焦会话，或解释斜杠命令。
func (model *terminalModel) handleLine(line string) tea.Cmd {
	model.dismissStartupActivity()
	command, argument, _ := strings.Cut(line, " ")
	argument = strings.TrimSpace(argument)
	if !strings.HasPrefix(command, "/") {
		if model.focused == "" {
			return model.startSessionWithMessage(line)
		}
		model.recordInput(line)
		model.clearTurnFeedback()
		return model.submit(model.focused, line)
	}
	switch command {
	case "/session":
		return model.handleSessionCommand(argument)
	case "/new":
		if argument != "" {
			model.addActivity(activityError, "错误：/new 不接受参数")
			model.renderConversation()
			return nil
		}
		return model.startSession()
	case "/help":
		model.addActivity(activityInfo, "/new  /session list|ID|delete [ID]  /help")
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
			model.loadInputHistory()
			model.watchSession("")
		} else {
			model.addActivity(activityInfo, "会话已删除："+value)
		}
	default:
		if verb != "" && value == "" {
			for _, session := range model.coordinator.Sessions() {
				if session.ID == verb {
					model.focused = session.ID
					model.clearTransientState()
					model.loadInputHistory()
					model.refresh()
					return model.watchSession(session.ID)
				}
			}
			model.addActivity(activityError, "错误：未找到会话 "+verb)
		} else {
			model.addActivity(activityError, "错误：会话命令为 list、ID 或 delete")
		}
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
	model.loadInputHistory()
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
	model.loadInputHistory()
	model.recordInput(message)
	model.refresh()
	return tea.Batch(model.watchSession(session.ID), model.submit(session.ID, message))
}

// loadInputHistory restores ordinary user messages for the focused session.
func (model *terminalModel) loadInputHistory() {
	model.inputHistory = nil
	model.historyIndex = -1
	model.historyDraft = ""
	if model == nil || model.coordinator == nil || model.focused == "" {
		return
	}
	for _, message := range model.coordinator.Messages(model.focused) {
		if message.Role != sessionstate.RoleUser {
			continue
		}
		value := strings.TrimSpace(message.Content)
		if value != "" && !strings.HasPrefix(value, "/") {
			model.inputHistory = append(model.inputHistory, value)
		}
	}
}

func (model *terminalModel) recordInput(value string) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") {
		return
	}
	if len(model.inputHistory) == 0 || model.inputHistory[len(model.inputHistory)-1] != value {
		model.inputHistory = append(model.inputHistory, value)
	}
	model.historyIndex = -1
	model.historyDraft = ""
}

func (model *terminalModel) resetInputNavigation() {
	if model.historyIndex >= 0 {
		model.historyIndex = -1
		model.historyDraft = ""
	}
}

func (model *terminalModel) previousInput() {
	if len(model.inputHistory) == 0 {
		return
	}
	if model.historyIndex < 0 {
		model.historyDraft = model.input.Value()
		model.historyIndex = len(model.inputHistory) - 1
	} else if model.historyIndex > 0 {
		model.historyIndex--
	}
	model.input.SetValue(model.inputHistory[model.historyIndex])
}

func (model *terminalModel) nextInput() {
	if model.historyIndex < 0 {
		return
	}
	if model.historyIndex < len(model.inputHistory)-1 {
		model.historyIndex++
		model.input.SetValue(model.inputHistory[model.historyIndex])
		return
	}
	model.historyIndex = -1
	model.input.SetValue(model.historyDraft)
	model.historyDraft = ""
}

// focusedSession 在不再次查询运行时的情况下查找当前会话快照。
func (model *terminalModel) focusedSession() *sessionstate.Session {
	for _, session := range model.sessions {
		if session.ID == model.focused {
			return session
		}
	}
	return nil
}

func (model *terminalModel) composerHit(mouse tea.MouseMsg) bool {
	startY := model.headerHeight() + model.viewport.Height
	return mouse.X >= 0 && mouse.X < model.contentWidth() && mouse.Y >= startY && mouse.Y < startY+model.composerHeight()
}

func (model *terminalModel) actionButtonHit(mouse tea.MouseMsg) bool {
	buttonWidth := lipgloss.Width(model.renderActionButton())
	buttonX := model.width - buttonWidth - 3
	buttonY := model.headerHeight() + model.viewport.Height + 1
	return mouse.X >= buttonX && mouse.X < buttonX+buttonWidth && mouse.Y == buttonY
}

func (model *terminalModel) handleActionButton() tea.Cmd {
	if model.turnRunning {
		if err := model.coordinator.PauseSession(model.focused); err != nil && !errors.Is(err, context.Canceled) {
			model.addActivity(activityError, err.Error())
		} else {
			model.addActivity(activityStatus, "正在暂停…")
		}
		model.refresh()
		return nil
	}
	line := strings.TrimSpace(model.input.Value())
	if line == "" {
		for _, session := range model.sessions {
			if session.ID == model.focused && session.ActiveTurn != nil && session.ActiveTurn.Status == sessionstate.TurnInterrupted {
				return model.resumeTurn(session.ID)
			}
		}
		return nil
	}
	model.input.Reset()
	return model.handleLine(line)
}

// toggleToolDetails 仅切换当前 TUI 的工具详情显示，不会变更 conversation 或 evidence。
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

// resumeTurn asynchronously continues a checkpointed interrupted turn.
func (model *terminalModel) resumeTurn(sessionID string) tea.Cmd {
	return func() tea.Msg {
		done := model.coordinator.ResumeTurn(model.ctx, sessionID)
		return turnCompleteMsg{sessionID: sessionID, err: <-done}
	}
}

// recordEvent 只追踪尚未结束的工具和非 conversation 的失败/中断状态，避免与已持久化消息重复。
func (model *terminalModel) recordEvent(event sessionstate.Event) {
	name := safeTerminalText(event.Message)
	switch event.Kind {
	case sessionstate.EventTurnStarted:
		model.activity = nil
		model.turnErrorShown = false
		model.turnRunning = true
		model.generating = true
	case sessionstate.EventAssistantDelta:
		model.generating = true
		if name != "" {
			model.addStreamText(name)
		}
	case sessionstate.EventAssistantMessage:
		model.generating = false
		model.streamText = ""
	case sessionstate.EventToolStarted:
		model.generating = false
		model.streamText = ""
		if name == "" {
			name = "工具"
		}
		model.runningTools[name]++
	case sessionstate.EventToolFinished:
		if name == "" {
			name = "工具"
		}
		model.finishRunningTool(name)
		if model.turnRunning && len(model.runningTools) == 0 {
			model.generating = true
		}
	case sessionstate.EventTurnFailed:
		model.turnRunning = false
		model.generating = false
		model.runningTools = make(map[string]int)
		model.addTurnError(name)
	case sessionstate.EventTurnFinished:
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

// dismissStartupActivity removes one-time startup diagnostics before the first user action.
func (model *terminalModel) dismissStartupActivity() {
	if len(model.startupActivity) == 0 {
		return
	}
	model.startupActivity = nil
}

// clearTransientState isolates command feedback and live execution state to the current focused session.
func (model *terminalModel) clearTransientState() {
	model.clearTurnFeedback()
	model.runningTools = make(map[string]int)
	model.turnRunning = false
	model.generating = false
}

func (model *terminalModel) clearTurnFeedback() {
	model.activity = nil
	model.streamText = ""
	model.turnErrorShown = false
}

// addStreamText coalesces provider deltas into one continuous assistant response.
func (model *terminalModel) addStreamText(value string) {
	value = safeTerminalText(value)
	if value == "" {
		return
	}
	model.streamText += value
}

func (model *terminalModel) addTurnError(value string) {
	if model.turnErrorShown {
		return
	}
	value = safeTerminalText(value)
	if value == "" {
		return
	}
	model.turnErrorShown = true
	model.addActivity(activityError, value)
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
func renderMessage(message sessionstate.Message, width int, showToolDetails bool, runningTools ...map[string]int) string {
	bodyWidth := max(10, width-2)
	running := map[string]int(nil)
	if len(runningTools) != 0 {
		running = runningTools[0]
	}
	switch message.Role {
	case sessionstate.RoleUser:
		return renderConversationBlock(userStyle.Render("YOU"), message.Content, bodyWidth, 0, userBodyStyle)
	case sessionstate.RoleAssistant:
		content := strings.TrimSpace(safeTerminalText(message.Content))
		parts := make([]string, 0, 2)
		if len(message.ToolCalls) != 0 {
			parts = append(parts, renderToolCalls(message.ToolCalls, bodyWidth, showToolDetails, running))
		}
		if content != "" {
			parts = append(parts, renderConversationBlock(modelStyle.Render("PENTGO"), content, bodyWidth, 0, modelBodyStyle))
		}
		if len(parts) == 0 {
			return mutedStyle.Render("PENTGO  ...")
		}
		return strings.Join(parts, "\n")
	case sessionstate.RoleTool:
		return renderToolResult(message, bodyWidth, showToolDetails)
	}
	return mutedStyle.Render(safeTerminalText(message.Content))
}

// renderToolCalls keeps tool planning scannable, marks only currently running calls, and reveals arguments in detail mode.
func renderToolCalls(calls []sessionstate.ToolCall, width int, showDetails bool, runningTools map[string]int) string {
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
			lines = append(lines, renderConversationBlock(mutedStyle.Render("Arguments:"), formatToolArguments(call), width, 8, toolBodyStyle))
		}
	}
	return strings.Join(lines, "\n")
}

// renderToolResult renders a one-line audit summary by default and bounded output in detail mode.
func renderToolResult(message sessionstate.Message, width int, showDetails bool) string {
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
		parts = append(parts, renderConversationBlock(mutedStyle.Render("Arguments:"), formatToolArguments(sessionstate.ToolCall{Arguments: message.ToolArguments}), width, 8, toolBodyStyle))
	}
	parts = append(parts, renderConversationBlock(mutedStyle.Render("Output:"), content, width, 8, toolBodyStyle))
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
func formatToolArguments(call sessionstate.ToolCall) string {
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

// safeTerminalText removes ANSI escape/control characters from untrusted conversation, event and command text.
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

// renderConversationBlock 统一正文换行、缩进和工具输出的最大显示行数。
func renderConversationBlock(title, content string, width, lineLimit int, bodyStyle lipgloss.Style) string {
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
