package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultContainsMinimalRuntimeConfiguration(t *testing.T) {
	agent := Default().Agent
	if agent.Provider != "openai" || agent.RequestTimeoutSeconds != 60 || agent.MaxOutputBytes != 65536 {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestDefaultLeavesOptionalToolProvidersDisabled(t *testing.T) {
	if len(Default().Agent.MCP) != 0 || len(Default().Agent.LocalTools) != 0 {
		t.Fatalf("default agent config = %+v", Default().Agent)
	}
}

func TestLoadPreservesNamedStdioMCPConfigs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"mcp":{"nmap":{"command":"/bin/nmap-mcp","args":["--stdio"],"env":{"NMAP_PATH":"/usr/bin/nmap"}},"browser":{"command":"/bin/browser-mcp","env":{"BROWSER_TOKEN":"TOKEN"}},"remote":{"type":"http","url":"http://127.0.0.1:80/mcp","headers":{"Authorization":"Bearer TOKEN"}}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agent.MCP) != 3 || cfg.Agent.MCP["nmap"].Command != "/bin/nmap-mcp" || len(cfg.Agent.MCP["nmap"].Args) != 1 || cfg.Agent.MCP["nmap"].Args[0] != "--stdio" || cfg.Agent.MCP["nmap"].Env["NMAP_PATH"] != "/usr/bin/nmap" || cfg.Agent.MCP["browser"].Env["BROWSER_TOKEN"] != "TOKEN" || cfg.Agent.MCP["remote"].Transport() != "http" || cfg.Agent.MCP["remote"].URL != "http://127.0.0.1:80/mcp" || cfg.Agent.MCP["remote"].Headers["Authorization"] != "Bearer TOKEN" {
		t.Fatalf("MCP = %+v", cfg.Agent.MCP)
	}
}

func TestLoadPreservesConfiguredLocalTools(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"local_tools":{"amass":{"command":"amass","description":"枚举已授权域名的子域。"},"custom_recon":{"command":"/opt/tools/recon"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agent.LocalTools) != 2 || cfg.Agent.LocalTools["amass"].Command != "amass" || cfg.Agent.LocalTools["amass"].Description != "枚举已授权域名的子域。" || cfg.Agent.LocalTools["custom_recon"].Command != "/opt/tools/recon" {
		t.Fatalf("local tools = %+v", cfg.Agent.LocalTools)
	}
}

func TestLoadRejectsInvalidLocalToolDefinitions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "empty command", data: `{"agent":{"local_tools":{"tool":{"command":" "}}}}`, want: "command is empty"},
		{name: "invalid name", data: `{"agent":{"local_tools":{"not valid":{"command":"tool"}}}}`, want: "invalid local tool name"},
		{name: "reserved name", data: `{"agent":{"local_tools":{"execute":{"command":"tool"}}}}`, want: "local tool name is reserved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, err := ConfigFile()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLoadUpgradesSingleStdioMCPConfigToDefaultServer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"agent":{"mcp":{"command":"/bin/fixture","args":["--stdio"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agent.MCP) != 1 || cfg.Agent.MCP["default"].Command != "/bin/fixture" {
		t.Fatalf("MCP = %+v", cfg.Agent.MCP)
	}
}

func TestLoadMergesLiveAgentConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"agent":{"provider":"anthropic","max_turns":7,"anthropic":{"model":"fixture-model"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Provider != "anthropic" || cfg.Agent.MaxTurns != 7 || cfg.Agent.Anthropic.Model != "fixture-model" {
		t.Fatalf("agent = %+v", cfg.Agent)
	}
}

func TestLoadPreservesOpenAIExtensionConfiguration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"openai":{"model":"deepseek-v4-flash","request_extra":{"thinking":{"type":"enabled"},"reasoning_effort":"high"},"response_reasoning_json_pointer":"/choices/0/message/reasoning_content"}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.OpenAI.RequestExtra["reasoning_effort"] != "high" || cfg.Agent.OpenAI.ResponseReasoningJSONPointer != "/choices/0/message/reasoning_content" {
		t.Fatalf("openai config = %#v", cfg.Agent.OpenAI)
	}
}
