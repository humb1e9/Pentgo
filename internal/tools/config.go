package tools

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var localToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Project fact 工具名。宿主为每个会话注入它们，任何外部/本地配置的工具都
// 不得占用这些名字。fact_tools.go 的 Name() 与 agent 的校验都引用这些常量，
// 保证重命名只改一处。
const (
	FactUpsertName = "upsert_project_fact"
	FactGetName    = "get_project_fact"
	FactListName   = "list_project_facts"
)

// reservedNames 拒绝本地工具配置使用宿主保留的工具名。
var reservedNames = func() map[string]bool {
	reserved := make(map[string]bool, len(workspaceBuiltinNames)+3)
	for _, builtin := range workspaceBuiltinNames {
		reserved[builtin] = true
	}
	reserved[FactUpsertName] = true
	reserved[FactGetName] = true
	reserved[FactListName] = true
	return reserved
}()

type LocalToolConfig struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type LocalTools map[string]LocalToolConfig

type MCPConfig struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

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

type MCPServers map[string]MCPConfig

type Config struct {
	MaxOutputBytes int        `json:"max_output_bytes"`
	Local          LocalTools `json:"local,omitempty"`
	MCP            MCPServers `json:"mcp,omitempty"`
}

func DefaultConfig() Config { return Config{MaxOutputBytes: 65536} }

func (config Config) Effective() Config {
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = DefaultConfig().MaxOutputBytes
	}
	if config.Local == nil {
		config.Local = LocalTools{}
	}
	if config.MCP == nil {
		config.MCP = MCPServers{}
	}
	return config
}

func (config Config) Validate() error {
	for _, name := range sortedLocalNames(config.Local) {
		if !localToolNamePattern.MatchString(name) {
			return fmt.Errorf("invalid local tool name: %q", name)
		}
		if reservedNames[name] {
			return fmt.Errorf("local tool name is reserved: %q", name)
		}
		if strings.TrimSpace(config.Local[name].Command) == "" {
			return fmt.Errorf("local tool %q command is empty", name)
		}
	}
	return nil
}

func sortedLocalNames(values LocalTools) []string {
	return slices.Sorted(maps.Keys(values))
}
