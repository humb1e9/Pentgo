package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const appConfigDirName = "pentgo"

// Config 保存终端 Agent 的用户级运行配置。
type Config struct {
	Agent AgentConfig `json:"agent"`
}

// AgentConfig 描述文本模型、执行限制和恢复策略。
type AgentConfig struct {
	Provider                  string              `json:"provider"`
	MaxTurns                  int                 `json:"max_turns"`
	RequestTimeoutSeconds     int                 `json:"request_timeout_seconds"`
	ExecutionTimeoutSeconds   int                 `json:"execution_timeout_seconds"`
	MaxOutputBytes            int                 `json:"max_output_bytes"`
	MaxParallelBlocks         int                 `json:"max_parallel_blocks"`
	NoCodeLimit               int                 `json:"no_code_limit"`
	ProviderRetryDelaySeconds int                 `json:"provider_retry_delay_seconds"`
	NetworkBackoffSeconds     int                 `json:"network_backoff_seconds"`
	SoftStuckTurns            int                 `json:"soft_stuck_turns"`
	HardStuckTurns            int                 `json:"hard_stuck_turns"`
	LineRepeatLimit           int                 `json:"line_repeat_limit"`
	ScanLineRepeatLimit       int                 `json:"scan_line_repeat_limit"`
	OpenAI                    ModelProviderConfig `json:"openai"`
	Anthropic                 ModelProviderConfig `json:"anthropic"`
}

// ModelProviderConfig 描述一个模型提供商的连接信息。
type ModelProviderConfig struct {
	BaseURL      string `json:"base_url"`
	Model        string `json:"model"`
	APIKey       string `json:"api_key,omitempty"`
	APIKeyEnv    string `json:"api_key_env"`
	ThinkingMode string `json:"thinking_mode,omitempty"`
}

// Default 返回单一终端 Agent Runtime 的默认配置。
func Default() Config {
	return Config{Agent: defaultAgentConfig()}
}

func defaultAgentConfig() AgentConfig {
	return AgentConfig{
		Provider:                  "openai",
		MaxTurns:                  20,
		RequestTimeoutSeconds:     60,
		ExecutionTimeoutSeconds:   1800,
		MaxOutputBytes:            65536,
		MaxParallelBlocks:         4,
		NoCodeLimit:               3,
		ProviderRetryDelaySeconds: 3,
		NetworkBackoffSeconds:     15,
		SoftStuckTurns:            3,
		HardStuckTurns:            5,
		LineRepeatLimit:           100,
		ScanLineRepeatLimit:       500,
		OpenAI: ModelProviderConfig{
			BaseURL:   "https://api.openai.com/v1",
			APIKeyEnv: "OPENAI_API_KEY",
		},
		Anthropic: ModelProviderConfig{
			BaseURL:   "https://api.anthropic.com",
			APIKeyEnv: "ANTHROPIC_API_KEY",
		},
	}
}

func normalizeAgentConfig(agent *AgentConfig) {
	defaults := defaultAgentConfig()
	if agent.Provider == "" {
		agent.Provider = defaults.Provider
	}
	if agent.MaxTurns <= 0 {
		agent.MaxTurns = defaults.MaxTurns
	}
	if agent.RequestTimeoutSeconds <= 0 {
		agent.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if agent.ExecutionTimeoutSeconds <= 0 {
		agent.ExecutionTimeoutSeconds = defaults.ExecutionTimeoutSeconds
	}
	if agent.MaxOutputBytes <= 0 {
		agent.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if agent.MaxParallelBlocks <= 0 {
		agent.MaxParallelBlocks = defaults.MaxParallelBlocks
	}
	if agent.NoCodeLimit <= 0 {
		agent.NoCodeLimit = defaults.NoCodeLimit
	}
	if agent.ProviderRetryDelaySeconds <= 0 {
		agent.ProviderRetryDelaySeconds = defaults.ProviderRetryDelaySeconds
	}
	if agent.NetworkBackoffSeconds <= 0 {
		agent.NetworkBackoffSeconds = defaults.NetworkBackoffSeconds
	}
	if agent.SoftStuckTurns <= 0 {
		agent.SoftStuckTurns = defaults.SoftStuckTurns
	}
	if agent.HardStuckTurns <= 0 {
		agent.HardStuckTurns = defaults.HardStuckTurns
	}
	if agent.LineRepeatLimit <= 0 {
		agent.LineRepeatLimit = defaults.LineRepeatLimit
	}
	if agent.ScanLineRepeatLimit <= 0 {
		agent.ScanLineRepeatLimit = defaults.ScanLineRepeatLimit
	}
	normalizeModelProviderConfig(&agent.OpenAI, defaults.OpenAI)
	normalizeModelProviderConfig(&agent.Anthropic, defaults.Anthropic)
}

func normalizeModelProviderConfig(provider *ModelProviderConfig, defaults ModelProviderConfig) {
	if provider.BaseURL == "" {
		provider.BaseURL = defaults.BaseURL
	}
	if provider.APIKeyEnv == "" {
		provider.APIKeyEnv = defaults.APIKeyEnv
	}
}

// ConfigDir 返回当前操作系统使用的 PentGo 配置目录，并仅支持 macOS 与 Linux。
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", appConfigDirName), nil
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, appConfigDirName), nil
		}
		return filepath.Join(home, ".config", appConfigDirName), nil
	default:
		return "", errors.New("unsupported platform")
	}
}

// ConfigFile 在 ConfigDir 下返回固定的 config.json 完整路径。
func ConfigFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load 读取并规范化用户配置；文件不存在时返回默认配置。
func Load() (Config, error) {
	path, err := ConfigFile()
	if err != nil {
		return Default(), err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	if !containsRootAgent(data) {
		if legacy, ok := legacyAgent(data); ok {
			cfg.Agent = migrateLegacyAgent(legacy)
		}
	}
	normalizeAgentConfig(&cfg.Agent)
	return cfg, nil
}

type legacyAgentConfig struct {
	Provider       string              `json:"provider"`
	MaxTurns       int                 `json:"max_turns"`
	TimeoutSeconds int                 `json:"timeout_seconds"`
	OpenAI         ModelProviderConfig `json:"openai"`
	Anthropic      ModelProviderConfig `json:"anthropic"`
}

func containsRootAgent(data []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(data, &fields) == nil && fields["agent"] != nil
}

func legacyAgent(data []byte) (legacyAgentConfig, bool) {
	var legacy struct {
		Recon struct {
			Agent *legacyAgentConfig `json:"agent"`
		} `json:"recon"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil || legacy.Recon.Agent == nil {
		return legacyAgentConfig{}, false
	}
	return *legacy.Recon.Agent, true
}

func migrateLegacyAgent(legacy legacyAgentConfig) AgentConfig {
	config := defaultAgentConfig()
	config.Provider = legacy.Provider
	config.MaxTurns = legacy.MaxTurns
	config.RequestTimeoutSeconds = legacy.TimeoutSeconds
	config.OpenAI = legacy.OpenAI
	config.Anthropic = legacy.Anthropic
	return config
}

// Save 创建配置目录，并以 0600 私有权限写入格式化后的 JSON 配置。
func Save(cfg Config) error {
	path, err := ConfigFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
