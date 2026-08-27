package model

import (
	"fmt"
	"strings"
)

// Config selects one model provider and contains its complete connection settings.
type Config struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	Thinking bool   `json:"thinking"`
}

func DefaultConfig() Config {
	return Config{Provider: "openai", BaseURL: "https://api.openai.com/v1"}
}

func (config Config) Validate() error {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider != "openai" && provider != "anthropic" {
		return fmt.Errorf("unsupported provider %q", config.Provider)
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return fmt.Errorf("model base_url is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return fmt.Errorf("model model is required")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("model api_key is required")
	}
	return nil
}
