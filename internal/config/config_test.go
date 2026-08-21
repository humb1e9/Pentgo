package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultContainsMinimalRuntimeConfiguration(t *testing.T) {
	agent := Default().Agent
	if agent.Provider != "openai" || agent.RequestTimeoutSeconds != 60 || agent.MaxOutputBytes != 65536 {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestDefaultLeavesMCPDisabled(t *testing.T) {
	if len(Default().Agent.MCP) != 0 {
		t.Fatalf("default MCP = %+v", Default().Agent.MCP)
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
