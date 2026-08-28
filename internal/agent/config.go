package agent

import (
	"pentgo/internal/model"
	"pentgo/internal/project"
	"pentgo/internal/tools"
)

// Config contains the validated values needed to run one project manager.
type Config struct {
	Model   model.Config
	Tools   tools.Config
	Project project.Config
}

// DefaultConfig returns the runtime defaults for direct construction and tests.
func DefaultConfig() Config {
	return Config{Model: model.DefaultConfig(), Tools: tools.DefaultConfig(), Project: project.DefaultConfig()}
}
