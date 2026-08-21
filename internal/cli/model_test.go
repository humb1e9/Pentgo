package cli

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"pentgo/internal/agent"
	"pentgo/internal/app"
	"pentgo/internal/config"
	"pentgo/internal/domain"
)

func TestTerminalModelRendersFocusedSession(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RenameSession(session.ID, "API reconnaissance"); err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, session.ID)
	model.width, model.height = 100, 32
	model.refresh()
	if view := model.View(); !strings.Contains(view, "API reconnaissance") || !strings.Contains(view, session.ID) {
		t.Fatalf("view = %q", view)
	}
}

func TestTerminalModelArrowKeysKeepCurrentSession(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.NewSession("first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.NewSession("second"); err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, first.ID)
	model.input.SetValue("draft")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(*terminalModel)
	if model.focused != first.ID {
		t.Fatalf("focused = %q, want %q", model.focused, first.ID)
	}
	if model.input.Value() != "draft" {
		t.Fatalf("input = %q", model.input.Value())
	}
}

func TestTerminalModelRenamesFocusedSession(t *testing.T) {
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
	model.handleSessionCommand("rename API reconnaissance")
	if renamed, err := coordinator.ResumeSession(session.ID); err != nil || renamed.Name != "API reconnaissance" {
		t.Fatalf("renamed/err = %#v/%v", renamed, err)
	}
}

func TestTerminalModelDeletesFocusedSessionWithoutSwitching(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.NewSession("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.NewSession("second")
	if err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, first.ID)
	model.handleSessionCommand("delete")
	if model.focused != "" {
		t.Fatalf("focused = %q, want empty", model.focused)
	}
	if sessions := coordinator.Sessions(); len(sessions) != 1 || sessions[0].ID != second.ID {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestTerminalModelNewCommandCreatesBlankSession(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	existing, err := coordinator.NewSession("existing")
	if err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, existing.ID)
	model.addActivity(activityError, "prior session failure")
	model.recordEvent(app.Event{Kind: app.EventToolStarted, Message: "http_probe"})
	model.handleLine("/new")
	if model.focused == existing.ID || model.focused == "" {
		t.Fatalf("focused = %q", model.focused)
	}
	created := model.focusedSession()
	if created == nil || created.Intent != "新会话" || created.Turns != 0 {
		t.Fatalf("created session = %+v", created)
	}
	if len(model.activity) != 0 || len(model.runningTools) != 0 {
		t.Fatalf("new session inherited transient state: activity=%#v tools=%#v", model.activity, model.runningTools)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "准备开始") {
		t.Fatalf("empty new session must show welcome card: %q", view)
	}
}

func TestTerminalModelNewCommandRejectsArguments(t *testing.T) {
	coordinator := app.New(config.Default(), t.TempDir(), app.Dependencies{})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	existing, err := coordinator.NewSession("existing")
	if err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), coordinator, existing.ID)
	command := model.handleLine("/new inspect TARGET")
	if command != nil || model.focused != existing.ID {
		t.Fatalf("command/focused = %v/%q", command != nil, model.focused)
	}
	if len(model.activity) == 0 || !strings.Contains(model.activity[len(model.activity)-1].text, "/new 不接受参数") {
		t.Fatalf("activity = %#v", model.activity)
	}
	if sessions := coordinator.Sessions(); len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestRenderMessageSeparatesTranscriptRoles(t *testing.T) {
	cases := []struct {
		name    string
		message agent.Message
		want    string
	}{
		{name: "user", message: agent.Message{Role: agent.RoleUser, Content: "inspect TARGET"}, want: "YOU\n  inspect TARGET"},
		{name: "assistant", message: agent.Message{Role: agent.RoleAssistant, Content: "正在检查入口。"}, want: "PENTGO\n  正在检查入口。"},
		{name: "tool", message: agent.Message{Role: agent.RoleTool, ToolName: "http_probe", Content: "200 OK"}, want: "TOOL  http_probe · 200 OK"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := strings.TrimRight(ansi.Strip(renderMessage(test.message, 80, false)), " ")
			if got != test.want {
				t.Fatalf("renderMessage() = %q, want %q", got, test.want)
			}
		})
	}
}

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

func TestRenderMessageBoundsToolOutput(t *testing.T) {
	content := strings.Repeat("line\n", 12)
	got := ansi.Strip(renderMessage(agent.Message{Role: agent.RoleTool, ToolName: "scan", Content: content}, 80, true))
	if !strings.Contains(got, "... 已截断") {
		t.Fatalf("renderMessage() = %q", got)
	}
	if strings.Count(got, "\n") != 11 {
		t.Fatalf("line count = %d, want 12", strings.Count(got, "\n")+1)
	}
}

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

func TestTerminalModelComposerKeepsUnicodeInputInsideBorder(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.width, model.height = 80, 20
	model.layout()
	for _, value := range []string{"abc", "你好世界", "abc你好", "🌍你好", "这是一个很长的中文输入内容，用于测试横向滚动"} {
		model.input.SetValue(value)
		if got, want := lipgloss.Width(model.input.View()), model.contentWidth()-2; got > want {
			t.Fatalf("value=%q input view width=%d, max inner width=%d", value, got, want)
		}
		line := strings.Split(model.renderComposer(), "\n")[0]
		if got, want := lipgloss.Width(line), model.width; got != want {
			t.Fatalf("value=%q first composer line width=%d, want %d", value, got, want)
		}
	}
}

func TestTerminalModelCtrlOTogglesToolDetails(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	message := agent.Message{Role: agent.RoleTool, ToolName: "http_probe", ToolArguments: map[string]any{"url": "https://example.test"}, Content: "200 OK\nserver: nginx"}
	if model.showToolDetails {
		t.Fatal("details should default to collapsed")
	}
	if compact := ansi.Strip(renderMessage(message, 80, model.showToolDetails)); strings.Contains(compact, "server: nginx") {
		t.Fatalf("compact rendering = %q", compact)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(*terminalModel)
	if !model.showToolDetails {
		t.Fatal("Ctrl+O should show details")
	}
	if detailed := ansi.Strip(renderMessage(message, 80, model.showToolDetails)); !strings.Contains(detailed, "server: nginx") {
		t.Fatalf("detailed rendering = %q", detailed)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(*terminalModel)
	if model.showToolDetails {
		t.Fatal("second Ctrl+O should collapse details")
	}
}

func TestTerminalModelShowsAndClearsGeneratingPlaceholder(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	if !model.generating {
		t.Fatal("turn start should show the generating placeholder")
	}
	if got := ansi.Strip(renderGeneratingPlaceholder()); got != "PENTGO · 正在生成…" {
		t.Fatalf("placeholder = %q", got)
	}

	model.recordEvent(app.Event{Kind: app.EventToolStarted, Message: "http_probe"})
	if model.generating {
		t.Fatal("tool activity should replace the generating placeholder")
	}
	model.recordEvent(app.Event{Kind: app.EventToolFinished, Message: "http_probe"})
	if !model.generating {
		t.Fatal("tool completion should restore the generating placeholder")
	}
	model.recordEvent(app.Event{Kind: app.EventAssistantMessage, Message: "完整回答"})
	if model.generating {
		t.Fatal("complete assistant message should remove the placeholder")
	}

	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	model.recordEvent(app.Event{Kind: app.EventTurnFailed, Message: "failed"})
	if model.generating {
		t.Fatal("failed turn should remove the placeholder")
	}
	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	model.clearTransientState()
	if model.generating {
		t.Fatal("session cleanup should remove the placeholder")
	}

	model.recordEvent(app.Event{Kind: app.EventTurnStarted})
	updated, _ := model.Update(turnCompleteMsg{sessionID: model.focused})
	model = updated.(*terminalModel)
	if model.generating || model.turnRunning || len(model.runningTools) != 0 {
		t.Fatalf("completion fallback leaked transient state: %+v", model)
	}
}

func TestTerminalModelTracksOnlyInFlightToolProgress(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(app.Event{Kind: app.EventToolStarted, Message: "http_probe"})
	if got := model.runningTools["http_probe"]; got != 1 {
		t.Fatalf("running tools = %#v", model.runningTools)
	}
	model.recordEvent(app.Event{Kind: app.EventToolFinished, Message: "http_probe", Output: "200 OK\nignored"})
	if len(model.runningTools) != 0 {
		t.Fatalf("completed tool must not remain transient: %#v", model.runningTools)
	}
	if len(model.activity) != 0 {
		t.Fatalf("completed tool must rely on transcript, activity = %#v", model.activity)
	}
}

func TestTerminalModelShowsRunningStateAndDoesNotDuplicateToolEvents(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.sessions = []*domain.Session{{ID: "session-1", Name: "test", ActiveTurn: &domain.Turn{Status: domain.TurnRunning}}}
	model.focused = "session-1"
	if status := model.sessionStatus(); !strings.Contains(status, "running") {
		t.Fatalf("status = %q", status)
	}

	message := agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{Name: "http_probe"}}}
	model.recordEvent(app.Event{Kind: app.EventToolStarted, Message: "http_probe"})
	if got := ansi.Strip(renderMessage(message, 80, false, model.runningTools)); !strings.Contains(got, "http_probe · running") {
		t.Fatalf("running tool call = %q", got)
	}
	model.recordEvent(app.Event{Kind: app.EventToolFinished, Message: "http_probe"})
	if got := ansi.Strip(renderMessage(message, 80, false, model.runningTools)); strings.Contains(got, "running") {
		t.Fatalf("completed tool call = %q", got)
	}
}

func TestRenderedTextStripsTerminalControlSequences(t *testing.T) {
	payload := "ok\x1b]52;c;clipboard\x07\x1b[2J\x1b[H\rhidden"
	message := agent.Message{Role: agent.RoleTool, ToolName: "probe\x1b[31m", Content: payload}
	got := renderMessage(message, 80, true)
	for _, forbidden := range []string{"\x1b", "\a", "\r"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("rendered unsafe control %q in %q", forbidden, got)
		}
	}
	stripped := ansi.Strip(got)
	for _, want := range []string{"probe", "ok", "hidden"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered content missing %q: %q", want, stripped)
		}
	}
	if strings.Contains(stripped, "clipboard") {
		t.Fatalf("OSC payload must be removed: %q", stripped)
	}
}

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
	if transcript := coordinator.Messages(session.ID); len(transcript) != 0 {
		t.Fatalf("new transcript = %#v", transcript)
	}
	model := newTerminalModel(context.Background(), coordinator, session.ID)
	model.width, model.height = 100, 32
	model.activity = []activityEntry{{level: activityInfo, text: "http_probe · 200 OK"}}
	model.refresh()

	view := ansi.Strip(model.View())
	if strings.Contains(view, "准备开始") {
		t.Fatalf("welcome must disappear after activity: %q", view)
	}
	for _, want := range []string{"http_probe", "200 OK"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %q", want, view)
		}
	}
}
