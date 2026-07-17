package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultContainsRootAgentRuntimeConfiguration(t *testing.T) {
	agent := Default().Agent
	if agent.Provider != "openai" || agent.MaxTurns != 20 || agent.RequestTimeoutSeconds != 60 {
		t.Fatalf("agent default = %+v", agent)
	}
	if agent.ExecutionTimeoutSeconds != 1800 || agent.MaxOutputBytes != 65536 || agent.MaxParallelBlocks != 4 {
		t.Fatalf("execution default = %+v", agent)
	}
	if agent.NoCodeLimit != 3 || agent.ProviderRetryDelaySeconds != 3 || agent.NetworkBackoffSeconds != 15 {
		t.Fatalf("recovery default = %+v", agent)
	}
	if agent.SoftStuckTurns != 3 || agent.HardStuckTurns != 5 || agent.LineRepeatLimit != 100 || agent.ScanLineRepeatLimit != 500 {
		t.Fatalf("stuck default = %+v", agent)
	}
	if agent.OpenAI.BaseURL != "https://api.openai.com/v1" || agent.OpenAI.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("openai default = %+v", agent.OpenAI)
	}
}

func TestDefaultDoesNotContainFixedPipelineConfiguration(t *testing.T) {
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"recon", "scan", "nuclei", "tscan", "subfinder"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("default config contains fixed pipeline field %q: %s", field, data)
		}
	}
}

func TestLoadMergesRootAgentConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"provider":"anthropic","max_turns":7,"execution_timeout_seconds":90,"max_parallel_blocks":2,"anthropic":{"model":"fixture-model"}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Provider != "anthropic" || cfg.Agent.MaxTurns != 7 || cfg.Agent.ExecutionTimeoutSeconds != 90 || cfg.Agent.MaxParallelBlocks != 2 {
		t.Fatalf("agent = %+v", cfg.Agent)
	}
	if cfg.Agent.Anthropic.Model != "fixture-model" || cfg.Agent.Anthropic.BaseURL != "https://api.anthropic.com" || cfg.Agent.Anthropic.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("anthropic = %+v", cfg.Agent.Anthropic)
	}
}

func TestLoadNormalizesInvalidRootAgentRuntimeValues(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"max_turns":0,"request_timeout_seconds":-1,"execution_timeout_seconds":0,"max_output_bytes":0,"max_parallel_blocks":0,"no_code_limit":0,"provider_retry_delay_seconds":0,"network_backoff_seconds":0,"soft_stuck_turns":0,"hard_stuck_turns":0,"line_repeat_limit":0,"scan_line_repeat_limit":0}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent != Default().Agent {
		t.Fatalf("agent = %+v, want defaults %+v", cfg.Agent, Default().Agent)
	}
}

func TestLoadMigratesLegacyReconAgentConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"recon":{"agent":{"provider":"openai","max_turns":9,"timeout_seconds":45,"openai":{"base_url":"https://api.deepseek.com","model":"legacy-model","api_key":"legacy-key","thinking_mode":"disabled"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Provider != "openai" || cfg.Agent.MaxTurns != 9 || cfg.Agent.RequestTimeoutSeconds != 45 {
		t.Fatalf("agent = %+v", cfg.Agent)
	}
	if cfg.Agent.OpenAI.BaseURL != "https://api.deepseek.com" || cfg.Agent.OpenAI.Model != "legacy-model" || cfg.Agent.OpenAI.APIKey != "legacy-key" || cfg.Agent.OpenAI.ThinkingMode != "disabled" {
		t.Fatalf("openai = %+v", cfg.Agent.OpenAI)
	}
}

func TestConfigDirUsesPentGoXDGNamespace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}

	xdgConfigHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error = %v", err)
	}
	want := filepath.Join(xdgConfigHome, "pentgo")
	if got != want {
		t.Fatalf("ConfigDir() = %q, want %q", got, want)
	}
}
