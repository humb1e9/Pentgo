package app

import (
	"pentgo/internal/model"
	"pentgo/internal/project"
	"pentgo/internal/tools"
)

// Config groups user-editable settings by the domain that consumes them.
type Config struct {
	Model   model.Config   `json:"model"`
	Tools   tools.Config   `json:"tools"`
	Project project.Config `json:"project"`
}

func Default() Config {
	return Config{
		Model:   model.DefaultConfig(),
		Tools:   tools.DefaultConfig(),
		Project: project.DefaultConfig(),
	}
}
