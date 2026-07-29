package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultContainsMinimalRuntimeConfiguration(t *testing.T) {
	agent := Default().Agent
	if agent.Provider != "openai" || agent.RequestTimeoutSeconds != 60 || agent.ExecutionTimeoutSeconds != 1800 || agent.MaxOutputBytes != 65536 || agent.NetworkBackoffSeconds != 15 {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestDefaultLeavesMCPDisabled(t *testing.T) {
	if Default().Agent.MCP != nil {
		t.Fatalf("default MCP = %+v", Default().Agent.MCP)
	}
}

func TestLoadPreservesSingleStdioMCPConfig(t *testing.T) {
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
	data := []byte(`{"agent":{"mcp":{"command":"/bin/fixture","args":["--stdio"],"env":{"FIXTURE_TOKEN":"TOKEN"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.MCP == nil || cfg.Agent.MCP.Command != "/bin/fixture" || len(cfg.Agent.MCP.Args) != 1 || cfg.Agent.MCP.Args[0] != "--stdio" || cfg.Agent.MCP.Env["FIXTURE_TOKEN"] != "TOKEN" {
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
	if err := os.WriteFile(path, []byte(`{"agent":{"provider":"anthropic","max_turns":7,"execution_timeout_seconds":90,"anthropic":{"model":"fixture-model"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Provider != "anthropic" || cfg.Agent.MaxTurns != 7 || cfg.Agent.ExecutionTimeoutSeconds != 90 || cfg.Agent.Anthropic.Model != "fixture-model" {
		t.Fatalf("agent = %+v", cfg.Agent)
	}
}
