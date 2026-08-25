package terminal

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStartupDiagnosticsDismissAfterNotice(t *testing.T) {
	model := newTerminalModel(context.Background(), nil, "")
	model.startupActivity = []activityEntry{{level: activityError, text: "技能已跳过"}}
	updated, _ := model.Update(dismissStartupActivityMsg{})
	if got := updated.(*terminalModel).startupActivity; len(got) != 0 {
		t.Fatalf("startup activity = %#v, want dismissed", got)
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
