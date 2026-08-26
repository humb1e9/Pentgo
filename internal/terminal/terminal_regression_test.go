package terminal

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"pentgo/internal/core"
	"pentgo/internal/project"
	app "pentgo/internal/project/runtime"
	sessionstate "pentgo/internal/project/session"
)

func TestResumeDefaultsToLatestNonEmptySession(t *testing.T) {
	root := t.TempDir()
	coordinator := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	active, err := coordinator.NewSession("persisted history")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RenameSession(active.ID, "old history"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.NewSession("新会话"); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.CloseProject(); err != nil {
		t.Fatal(err)
	}
	store, err := project.OpenProjectStore(filepath.Join(root, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.OpenConversation(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.Append(core.Message{Role: core.RoleUser, Content: "history"}); err != nil {
		t.Fatal(err)
	}
	_ = conversation.Close()
	_ = store.Close()
	resumed := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	defer resumed.CloseProject()
	if _, err := resumed.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	terminal := NewRuntimeTerminal(resumed, strings.NewReader("\n"), &output)
	selected, err := terminal.selectResumeSession()
	if err != nil {
		t.Fatal(err)
	}
	if selected != active.ID {
		t.Fatalf("selected session = %q, want persisted %q; output=%s", selected, active.ID, output.String())
	}
	if terminal.input == nil {
		t.Fatal("resume selection lost the TUI input")
	}
}

func TestStartupDiagnosticsDismissAfterNotice(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.startupActivity = []activityEntry{{level: activityError, text: "技能已跳过"}}
	updated, _ := model.Update(dismissStartupActivityMsg{})
	if got := updated.(*terminalModel).startupActivity; len(got) != 0 {
		t.Fatalf("startup activity = %#v, want dismissed", got)
	}
}

func TestRuntimeTerminalResumeRendersPersistedConversation(t *testing.T) {
	root := t.TempDir()
	first := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	if _, _, err := first.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := first.NewSession("resume")
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.OpenProjectStore(filepath.Join(root, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.OpenConversation(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := persisted.Append(core.Message{Role: core.RoleUser, Content: "terminal persisted question"}); err != nil {
		t.Fatal(err)
	}
	if err := persisted.Append(core.Message{Role: core.RoleAssistant, Content: "terminal persisted answer"}); err != nil {
		t.Fatal(err)
	}
	_ = persisted.Close()
	_ = store.Close()
	if err := first.CloseProject(); err != nil {
		t.Fatal(err)
	}

	resumed := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	var output bytes.Buffer
	terminal := NewRuntimeTerminal(resumed, strings.NewReader("\n\x03"), &output)
	if err := terminal.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(output.String())
	for _, want := range []string{"terminal persisted question", "terminal persisted answer"} {
		if !strings.Contains(view, want) {
			t.Fatalf("RuntimeTerminal.Resume output missing %q:\n%s", want, view)
		}
	}
	if sessions := resumed.Sessions(); len(sessions) != 0 {
		t.Fatalf("resume left project open: %#v", sessions)
	}
}

func TestResumedSessionRendersPersistedConversation(t *testing.T) {
	root := t.TempDir()
	first := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	if _, _, err := first.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := first.NewSession("resume")
	if err != nil {
		t.Fatal(err)
	}
	conversation := first.Messages(session.ID)
	if len(conversation) != 0 {
		t.Fatalf("new conversation = %#v", conversation)
	}
	store, err := project.OpenProjectStore(filepath.Join(root, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.OpenConversation(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := persisted.Append(core.Message{Role: core.RoleUser, Content: "persisted question"}); err != nil {
		t.Fatal(err)
	}
	if err := persisted.Append(core.Message{Role: core.RoleAssistant, Content: "persisted answer"}); err != nil {
		t.Fatal(err)
	}
	_ = persisted.Close()
	_ = store.Close()
	if err := first.CloseProject(); err != nil {
		t.Fatal(err)
	}

	resumed := app.NewManager(testRuntimeConfig(), root, app.Dependencies{SkillsFS: fstest.MapFS{}})
	defer resumed.CloseProject()
	if _, err := resumed.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.ResumeSession(session.ID); err != nil {
		t.Fatal(err)
	}
	model := newTerminalModel(context.Background(), resumed, session.ID)
	model.width, model.height = 100, 24
	model.layout()
	model.refresh()
	view := ansi.Strip(model.View())
	for _, want := range []string{"persisted question", "persisted answer"} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed view missing %q:\n%s", want, view)
		}
	}
}

func TestTurnErrorAppearsOnceAndClearsForNextTurn(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "session-1")
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventTurnStarted})
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventTurnFailed, Message: "provider failed"})
	updated, _ := model.Update(turnCompleteMsg{sessionID: "session-1", err: errors.New("provider failed")})
	model = updated.(*terminalModel)
	if len(model.activity) != 1 || model.activity[0].level != activityError {
		t.Fatalf("duplicate turn errors = %#v", model.activity)
	}
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventTurnStarted})
	if len(model.activity) != 0 || model.turnErrorShown {
		t.Fatalf("next turn retained error state: %#v shown=%v", model.activity, model.turnErrorShown)
	}
}

func TestStreamDeltasRenderAsContinuousText(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventAssistantDelta, Message: "正在"})
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventAssistantDelta, Message: "检查目标"})
	if model.streamText != "正在检查目标" {
		t.Fatalf("stream text = %q", model.streamText)
	}
	view := ansi.Strip(renderConversationBlock("PENTGO", model.streamText, 100, 0, modelBodyStyle))
	if !strings.Contains(view, "正在检查目标") {
		t.Fatalf("streamed view split fragments: %q", view)
	}
	model.recordEvent(sessionstate.Event{Kind: sessionstate.EventAssistantMessage})
	if model.streamText != "" {
		t.Fatalf("completed stream not cleared: %q", model.streamText)
	}
}

func TestMouseWheelScrollsHistoryWithoutEditingInput(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.width, model.height = 100, 12
	model.layout()
	model.viewport.SetContent(strings.Repeat("persisted history\n", 100))
	model.viewport.GotoBottom()
	model.input.SetValue("draft")
	before := model.viewport.YOffset

	updated, _ := model.Update(tea.MouseMsg{Type: tea.MouseWheelUp, Action: tea.MouseActionPress})
	model = updated.(*terminalModel)
	if model.viewport.YOffset >= before {
		t.Fatalf("wheel up offset = %d, want less than %d", model.viewport.YOffset, before)
	}
	if model.input.Value() != "draft" {
		t.Fatalf("mouse input changed composer: %q", model.input.Value())
	}
}

func TestHistoryNavigationRevealsResumedMessages(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.width, model.height = 100, 12
	model.layout()
	model.viewport.SetContent(strings.Repeat("persisted history\n", 100))
	model.viewport.GotoBottom()
	before := model.viewport.YOffset

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(*terminalModel)
	if model.viewport.YOffset >= before {
		t.Fatalf("page up offset = %d, want less than %d", model.viewport.YOffset, before)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	if !updated.(*terminalModel).viewport.AtTop() {
		t.Fatal("home did not reveal the oldest history")
	}
}
