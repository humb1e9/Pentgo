package project

import "fmt"

const toolResultPruneMarker = "\n\n[... tool result middle pruned ...]\n\n"

// Config controls the project turn loop and persistent context budget.
type Config struct {
	MaxTurns int           `json:"max_turns"`
	Context  ContextConfig `json:"context"`
}

type ContextConfig struct {
	ContextWindow            int     `json:"context_window"`
	ThresholdRatio           float64 `json:"threshold_ratio"`
	RetainRatio              float64 `json:"retain_ratio"`
	ToolResultThresholdChars int     `json:"tool_result_threshold_chars"`
	ToolResultHeadChars      int     `json:"tool_result_head_chars"`
	ToolResultTailChars      int     `json:"tool_result_tail_chars"`
	CheckpointMaxTokens      int     `json:"checkpoint_max_tokens"`
}

func DefaultConfig() Config {
	return Config{MaxTurns: 1000, Context: ContextConfig{ContextWindow: 128000, ThresholdRatio: .8, RetainRatio: .16, ToolResultThresholdChars: 8192, ToolResultHeadChars: 4096, ToolResultTailChars: 1024, CheckpointMaxTokens: 8192}}
}

func (config Config) Effective() Config {
	defaults := DefaultConfig()
	if config.MaxTurns <= 0 {
		config.MaxTurns = defaults.MaxTurns
	}
	if config.Context.ContextWindow == 0 {
		config.Context = defaults.Context
		return config
	}
	if config.Context.ThresholdRatio == 0 {
		config.Context.ThresholdRatio = defaults.Context.ThresholdRatio
	}
	if config.Context.RetainRatio == 0 {
		config.Context.RetainRatio = defaults.Context.RetainRatio
	}
	if config.Context.ToolResultThresholdChars == 0 {
		config.Context.ToolResultThresholdChars = defaults.Context.ToolResultThresholdChars
	}
	if config.Context.ToolResultHeadChars == 0 {
		config.Context.ToolResultHeadChars = defaults.Context.ToolResultHeadChars
	}
	if config.Context.ToolResultTailChars == 0 {
		config.Context.ToolResultTailChars = defaults.Context.ToolResultTailChars
	}
	if config.Context.CheckpointMaxTokens == 0 {
		config.Context.CheckpointMaxTokens = defaults.Context.CheckpointMaxTokens
	}
	return config
}

func (config Config) Validate() error {
	config = config.Effective()
	policy := config.Context
	if policy.ContextWindow <= 0 {
		return fmt.Errorf("context_window must be > 0")
	}
	if policy.ThresholdRatio <= 0 || policy.ThresholdRatio > 1 {
		return fmt.Errorf("threshold_ratio must be in (0, 1]")
	}
	if policy.RetainRatio <= 0 || policy.RetainRatio >= policy.ThresholdRatio {
		return fmt.Errorf("retain_ratio must be in (0, threshold_ratio)")
	}
	if policy.ToolResultHeadChars+len([]rune(toolResultPruneMarker))+policy.ToolResultTailChars > policy.ToolResultThresholdChars {
		return fmt.Errorf("tool result threshold/head/tail settings are invalid")
	}
	return nil
}
