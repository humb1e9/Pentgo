package project

import "fmt"

// Config controls project turns and the model-visible conversation budget.
type Config struct {
	MaxTurns int           `json:"max_turns"`
	Context  ContextConfig `json:"context"`
}

// ContextConfig retains recent raw messages and a rolling summary within one model input budget.
type ContextConfig struct {
	ContextWindow    int `json:"context_window"`
	RecentMessages   int `json:"recent_messages"`
	SummaryMaxTokens int `json:"summary_max_tokens"`
}

func DefaultConfig() Config {
	return Config{MaxTurns: 1000, Context: ContextConfig{ContextWindow: 256000, RecentMessages: 32, SummaryMaxTokens: 8192}}
}

func (config Config) Effective() Config {
	defaults := DefaultConfig()
	if config.MaxTurns <= 0 {
		config.MaxTurns = defaults.MaxTurns
	}
	if config.Context.ContextWindow <= 0 {
		config.Context.ContextWindow = defaults.Context.ContextWindow
	}
	if config.Context.RecentMessages <= 0 {
		config.Context.RecentMessages = defaults.Context.RecentMessages
	}
	if config.Context.SummaryMaxTokens <= 0 {
		config.Context.SummaryMaxTokens = defaults.Context.SummaryMaxTokens
	}
	return config
}

func (config Config) Validate() error {
	config = config.Effective()
	if config.Context.ContextWindow <= 0 {
		return fmt.Errorf("context_window must be > 0")
	}
	if config.Context.RecentMessages < 2 {
		return fmt.Errorf("recent_messages must be at least 2")
	}
	if config.Context.SummaryMaxTokens <= 0 {
		return fmt.Errorf("summary_max_tokens must be > 0")
	}
	return nil
}
