package bootstrap

import (
	"io/fs"
	"time"

	"pentgo/internal/model"
	projectruntime "pentgo/internal/project/runtime"
)

// Application owns process-level composition. The embedded Manager owns the
// project lifecycle while bootstrap supplies the validated configuration.
type Application struct {
	*projectruntime.Manager
	Config Config
}

// NewApplication builds the runtime from the new domain-grouped configuration.
func NewApplication(cfg Config, root string, skills fs.FS) *Application {
	return &Application{Manager: projectruntime.NewManager(projectruntime.Config{
		Model: cfg.Model, Tools: cfg.Tools, Project: cfg.Project,
	}, root, projectruntime.Dependencies{
		Clock:    func() time.Time { return time.Now().UTC() },
		NewModel: model.New,
		SkillsFS: skills,
	}), Config: cfg}
}
