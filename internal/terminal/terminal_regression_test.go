package terminal

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"pentgo/internal/core"
	"pentgo/internal/project"
	app "pentgo/internal/project/runtime"
)

func TestStartupDiagnosticsDismissAfterNotice(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.startupActivity = []activityEntry{{level: activityError, text: "技能已跳过"}}
	updated, _ := model.Update(dismissStartupActivityMsg{})
	if got := updated.(*terminalModel).startupActivity; len(got) != 0 {
		t.Fatalf("startup activity = %#v, want dismissed", got)
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
