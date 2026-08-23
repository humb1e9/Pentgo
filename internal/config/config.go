package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// appConfigDirName 是用户配置根目录下使用的操作系统专属目录名。
const appConfigDirName = "pentgo"

var (
	localToolNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	localToolReservedNames = map[string]bool{
		"ls": true, "read_file": true, "write_file": true, "edit_file": true,
		"glob": true, "grep": true, "execute": true,
		"upsert_project_fact":    true,
		"get_project_fact":       true,
		"list_project_facts":     true,
		"search_project_facts":   true,
		"deprecate_project_fact": true,
		"restore_project_fact":   true,
	}
)

// Config 保存终端 Agent 的用户级运行配置。
type Config struct {
	Agent AgentConfig `json:"agent"`
}

// AgentConfig 描述 Eino 工具调用模型、本机 CLI 工具与外部 MCP 连接。
type AgentConfig struct {
	Provider              string              `json:"provider"`
	MaxTurns              int                 `json:"max_turns"`
	RequestTimeoutSeconds int                 `json:"request_timeout_seconds"`
	MaxOutputBytes        int                 `json:"max_output_bytes"`
	OpenAI                ModelProviderConfig `json:"openai"`
	Anthropic             ModelProviderConfig `json:"anthropic"`
	MCP                   MCPServers          `json:"mcp,omitempty"`
	LocalTools            LocalTools          `json:"local_tools,omitempty"`
	Context               AgentContextConfig  `json:"context,omitempty"`
}

// AgentContextConfig configures optional persistent context budgeting. A zero
// ContextWindow preserves the legacy full-transcript replay behavior.
type AgentContextConfig struct {
	ContextWindow            int     `json:"context_window,omitempty"`
	ThresholdRatio           float64 `json:"threshold_ratio,omitempty"`
	RetainRatio              float64 `json:"retain_ratio,omitempty"`
	FactIndexRatio           float64 `json:"fact_index_ratio,omitempty"`
	ToolResultThresholdChars int     `json:"tool_result_threshold_chars,omitempty"`
	ToolResultHeadChars      int     `json:"tool_result_head_chars,omitempty"`
	ToolResultTailChars      int     `json:"tool_result_tail_chars,omitempty"`
	CheckpointMaxTokens      int     `json:"checkpoint_max_tokens,omitempty"`
	CheckpointProvider       string  `json:"checkpoint_provider,omitempty"`
	CheckpointModel          string  `json:"checkpoint_model,omitempty"`
}

const toolResultPruneMarker = "\n\n[... tool result middle pruned ...]\n\n"

// UnmarshalJSON rejects the removed Phase 1 blackboard_ratio explicitly so
// operators migrate intent instead of silently getting a different budget.
func (config *AgentContextConfig) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, legacy := fields["blackboard_ratio"]; legacy {
		return fmt.Errorf("blackboard_ratio was removed; use fact_index_ratio")
	}
	type raw AgentContextConfig
	var decoded raw
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*config = AgentContextConfig(decoded)
	return nil
}

// Enabled reports whether context budgeting is active for this agent.
func (config AgentContextConfig) Enabled() bool {
	return config.ContextWindow > 0
}

// Effective applies the Phase 1 defaults to enabled policies only. Disabled
// policies are returned unchanged so callers retain legacy replay behavior.
func (config AgentContextConfig) Effective() AgentContextConfig {
	if !config.Enabled() {
		return config
	}
	if config.ThresholdRatio == 0 {
		config.ThresholdRatio = 0.80
	}
	if config.RetainRatio == 0 {
		config.RetainRatio = 0.16
	}
	if config.FactIndexRatio == 0 {
		config.FactIndexRatio = 0.08
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

// LocalToolConfig declares one user-managed local CLI that LocalRegistry
// exposes to the model. command is executed directly without a shell.
type LocalToolConfig struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// LocalTools maps the model-visible name to its user-managed CLI command.
type LocalTools map[string]LocalToolConfig

// MCPConfig 配置一个标准输入输出、Streamable HTTP 或旧版 SSE MCP 服务。
type MCPConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Transport 将别名和推导出的默认值解析为 stdio、http 或 sse。
func (config MCPConfig) Transport() string {
	if kind := strings.ToLower(strings.TrimSpace(config.Type)); kind != "" {
		if kind == "streamable-http" || kind == "streamable_http" {
			return "http"
		}
		return kind
	}
	if strings.TrimSpace(config.Command) != "" {
		return "stdio"
	}
	if strings.TrimSpace(config.URL) != "" {
		return "http"
	}
	return ""
}

// MCPServers 接受当前的具名服务结构，并在加载时将旧版单服务对象升级到稳定的
// "default" 命名空间。
type MCPServers map[string]MCPConfig

// UnmarshalJSON 同时接受旧版单服务对象以保持配置兼容性，
// 并将其存储在稳定的默认服务名称下。
func (servers *MCPServers) UnmarshalJSON(data []byte) error {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	if _, legacy := entries["command"]; legacy {
		var single MCPConfig
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		*servers = MCPServers{"default": single}
		return nil
	}
	var named map[string]MCPConfig
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	*servers = named
	return nil
}

// ModelProviderConfig 描述一个模型提供商的连接信息。
type ModelProviderConfig struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`
	APIKeyEnv string `json:"api_key_env"`
	// ThinkingMode is intentionally deprecated. It is retained only so the
	// runtime can return a migration error instead of silently ignoring it.
	ThinkingMode string `json:"thinking_mode,omitempty"`
	// RequestExtra is passed verbatim as top-level fields in an OpenAI Chat
	// Completions request. It is for provider extensions such as reasoning.
	RequestExtra map[string]any `json:"request_extra,omitempty"`
	// ResponseReasoningJSONPointer identifies a non-standard reasoning string
	// in a non-streaming JSON response, using RFC 6901 JSON Pointer syntax.
	ResponseReasoningJSONPointer string `json:"response_reasoning_json_pointer,omitempty"`
	// StreamResponseReasoningJSONPointer is the equivalent pointer for each
	// streaming response chunk.
	StreamResponseReasoningJSONPointer string `json:"stream_response_reasoning_json_pointer,omitempty"`
}

// Default 返回单一终端 Agent Runtime 的默认配置。
func Default() Config {
	return Config{Agent: defaultAgentConfig()}
}

// defaultAgentConfig 集中定义加载和测试使用的默认 Provider 值。
func defaultAgentConfig() AgentConfig {
	return AgentConfig{
		Provider:              "openai",
		MaxTurns:              0,
		RequestTimeoutSeconds: 60,
		MaxOutputBytes:        65536,
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

// normalizeAgentConfig 仅补充缺失或无效的运行默认值。
// validateLocalTools rejects configuration that cannot become stable model tool
// definitions. It deliberately does not inspect or execute user commands.
func validateLocalTools(tools LocalTools) error {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !localToolNamePattern.MatchString(name) {
			return fmt.Errorf("invalid local tool name: %q", name)
		}
		if localToolReservedNames[name] {
			return fmt.Errorf("local tool name is reserved: %q", name)
		}
		if strings.TrimSpace(tools[name].Command) == "" {
			return fmt.Errorf("local tool %q command is empty", name)
		}
	}
	return nil
}

func validateAgentContextConfig(policy AgentContextConfig) error {
	if policy.ContextWindow < 0 {
		return fmt.Errorf("context_window must be >= 0")
	}
	if !policy.Enabled() {
		return nil
	}
	policy = policy.Effective()
	if policy.ThresholdRatio <= 0 || policy.ThresholdRatio > 1 {
		return fmt.Errorf("threshold_ratio must be in (0, 1]")
	}
	if policy.RetainRatio <= 0 || policy.RetainRatio >= policy.ThresholdRatio {
		return fmt.Errorf("retain_ratio must be in (0, threshold_ratio)")
	}
	if policy.FactIndexRatio <= 0 || policy.FactIndexRatio >= policy.ThresholdRatio {
		return fmt.Errorf("fact_index_ratio must be in (0, threshold_ratio)")
	}
	if policy.ToolResultThresholdChars <= 0 || policy.ToolResultHeadChars < 0 || policy.ToolResultTailChars < 0 || policy.ToolResultHeadChars+len([]rune(toolResultPruneMarker))+policy.ToolResultTailChars > policy.ToolResultThresholdChars {
		return fmt.Errorf("tool result threshold/head/tail settings are invalid")
	}
	if policy.CheckpointMaxTokens <= 0 {
		return fmt.Errorf("checkpoint_max_tokens must be > 0")
	}
	providerSet := strings.TrimSpace(policy.CheckpointProvider) != ""
	modelSet := strings.TrimSpace(policy.CheckpointModel) != ""
	if providerSet != modelSet {
		return fmt.Errorf("checkpoint_provider and checkpoint_model must be configured together")
	}
	if providerSet {
		switch strings.ToLower(strings.TrimSpace(policy.CheckpointProvider)) {
		case "openai", "anthropic":
		default:
			return fmt.Errorf("checkpoint_provider must be openai or anthropic")
		}
	}
	return nil
}

func normalizeAgentConfig(agent *AgentConfig) {
	defaults := defaultAgentConfig()
	if agent.Provider == "" {
		agent.Provider = defaults.Provider
	}
	if agent.RequestTimeoutSeconds <= 0 {
		agent.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if agent.MaxOutputBytes <= 0 {
		agent.MaxOutputBytes = defaults.MaxOutputBytes
	}
	normalizeModelProviderConfig(&agent.OpenAI, defaults.OpenAI)
	normalizeModelProviderConfig(&agent.Anthropic, defaults.Anthropic)
}

// normalizeModelProviderConfig 补充端点和环境变量密钥默认值。
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
	if err := validateLocalTools(cfg.Agent.LocalTools); err != nil {
		return Default(), err
	}
	if err := validateAgentContextConfig(cfg.Agent.Context); err != nil {
		return Default(), err
	}
	normalizeAgentConfig(&cfg.Agent)
	cfg.Agent.Context = cfg.Agent.Context.Effective()
	return cfg, nil
}
