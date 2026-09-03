package app

import (
	"io/fs"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/model"
)

// NewApplication builds the project runtime from the validated configuration.
func NewApplication(cfg agent.Config, root string, skills fs.FS) *agent.Manager {
	return agent.NewManager(cfg, root, agent.Dependencies{
		Clock:    func() time.Time { return time.Now().UTC() },
		NewModel: model.New,
		SkillsFS: skills,
	})
}
