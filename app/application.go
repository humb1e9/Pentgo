package app

import (
	"io/fs"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/model"
)

// Application owns process-level composition. The embedded Manager owns the
// project lifecycle while bootstrap supplies the validated configuration.
type Application struct {
	*agent.Manager
	Config Config
}

// NewApplication builds the runtime from the new domain-grouped configuration.
func NewApplication(cfg Config, root string, skills fs.FS) *Application {
	return &Application{Manager: agent.NewManager(agent.Config{
		Model: cfg.Model, Tools: cfg.Tools, Project: cfg.Project,
	}, root, agent.Dependencies{
		Clock:    func() time.Time { return time.Now().UTC() },
		NewModel: model.New,
		SkillsFS: skills,
	}), Config: cfg}
}
