package runtime

import (
	"pentgo/internal/model"
	"pentgo/internal/project"
	projectcontext "pentgo/internal/project/context"
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

func projectContextPolicy(policy project.ContextConfig) projectcontext.Config {
	return projectcontext.Config{
		ContextWindow: policy.ContextWindow, ThresholdRatio: policy.ThresholdRatio,
		RetainRatio: policy.RetainRatio, ToolResultThresholdChars: policy.ToolResultThresholdChars,
		ToolResultHeadChars: policy.ToolResultHeadChars, ToolResultTailChars: policy.ToolResultTailChars,
		CheckpointMaxTokens: policy.CheckpointMaxTokens,
	}
}
