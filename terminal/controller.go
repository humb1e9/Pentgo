package terminal

import (
	"context"

	projectmodel "pentgo/internal/project"
	sessionstate "pentgo/internal/session"
	"pentgo/internal/tools"
)

// Controller is the complete runtime surface consumed by the terminal UI.
// bootstrap.Application implements it; the UI never opens SQLite, models, or tools.
type Controller interface {
	OpenCurrentProject(context.Context) (*projectmodel.Project, error)
	OpenOrCreateWorkspace(context.Context) (*projectmodel.Project, bool, error)
	CloseProject() error
	NewSession(string, ...string) (*sessionstate.Session, error)
	ResumeSession(string) (*sessionstate.Session, error)
	DeleteSession(string) error
	CurrentProject() (*projectmodel.Project, bool)
	Sessions() []*sessionstate.Session
	Messages(string) []sessionstate.Message
	Events(string) <-chan sessionstate.Event
	SkillDiagnostics() []tools.Diagnostic
	PauseSession(string) error
	ResumeTurn(context.Context, string) <-chan error
	Submit(context.Context, string, string) <-chan error
}
