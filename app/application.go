package app

import (
	"io/fs"

	"pentgo/internal/agent"
	"pentgo/internal/model"
)

// NewApplication builds the project runtime from the validated configuration.
func NewApplication(cfg agent.Config, root string, skills fs.FS) *agent.Manager {
	return agent.NewManager(cfg, root, agent.Dependencies{
		NewModel: model.New,
		SkillsFS: skills,
	})
}
