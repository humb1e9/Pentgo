package tools

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var localToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var reservedNames = map[string]bool{
	"ls": true, "read_file": true, "write_file": true, "edit_file": true,
	"glob": true, "grep": true, "execute": true,
	"upsert_project_fact": true, "get_project_fact": true, "list_project_facts": true,
}

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
