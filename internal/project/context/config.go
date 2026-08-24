package context

const toolResultMiddleMarker = "\n\n[... tool result middle pruned ...]\n\n"

// Config controls persistent context budgeting for one project turn.
type Config struct {
	ContextWindow            int
	ThresholdRatio           float64
	RetainRatio              float64
	ToolResultThresholdChars int
	ToolResultHeadChars      int
	ToolResultTailChars      int
	CheckpointMaxTokens      int
}

func (config Config) Enabled() bool { return config.ContextWindow > 0 }
func (config Config) Effective() Config {
	if !config.Enabled() {
		return config
	}
	if config.ThresholdRatio == 0 {
		config.ThresholdRatio = .80
	}
	if config.RetainRatio == 0 {
		config.RetainRatio = .16
	}
	if config.ToolResultThresholdChars == 0 {
		config.ToolResultThresholdChars = 8192
	}
	if config.ToolResultHeadChars == 0 {
		config.ToolResultHeadChars = 4096
	}
	if config.ToolResultTailChars == 0 {
		config.ToolResultTailChars = 1024
	}
	if config.CheckpointMaxTokens == 0 {
		config.CheckpointMaxTokens = 8192
	}
	return config
}
