package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pentgo/internal/config"
)

func isolateConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	return root
}

func TestRunREPLNaturalLanguageTaskCreatesRuntimeEngagement(t *testing.T) {
	isolateConfig(t)
	model := newModelServer(t)
	output := t.TempDir()
	t.Chdir(output)
	writeREPLConfig(t, model.URL)
	var stdout, stderr bytes.Buffer

	code := runREPL(context.Background(), strings.NewReader("对 http://example.test 做检查\n"), &stdout, &stderr, make(chan os.Signal))
	if code != 0 {
		t.Fatalf("exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("engagement directories = %v", entries)
	}
	for _, name := range []string{"session.json", "report.md", "evidence/agent-turn-001-block-001.json", "work/turn-001-block-001.py"} {
		if _, err := os.Stat(filepath.Join(output, entries[0].Name(), name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	reportBody, err := os.ReadFile(filepath.Join(output, entries[0].Name(), "report.md"))
	if err != nil || !strings.HasPrefix(string(reportBody), "# PentGo Agent Report") || !strings.Contains(string(reportBody), "## 已验证发现") {
		t.Fatalf("report/err = %q/%v", reportBody, err)
	}
}

func TestRunREPLMalformedConfigWarnsAndUsesDefaults(t *testing.T) {
	isolateConfig(t)
	configPath, err := config.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr, make(chan os.Signal))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "config") || !strings.Contains(stderr.String(), "using defaults") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeREPLConfig(t *testing.T, baseURL string) {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.OpenAI.BaseURL = baseURL
	cfg.Agent.OpenAI.Model = "fixture"
	cfg.Agent.OpenAI.APIKey = "fixture-key"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func newModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	responses := []string{
		"{\"choices\":[{\"message\":{\"content\":\"```python\\nimport os\\nprint(os.environ['PENTGO_TARGET'])\\n```\"}}]}",
		`{"choices":[{"message":{"content":"TASK_COMPLETE"}}]}`,
		`{"choices":[{"message":{"content":"NO_FINDINGS"}}]}`,
		`{"choices":[{"message":{"content":"## 执行摘要\n已完成检查。"}}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if len(responses) == 0 {
			t.Fatal("unexpected model request")
		}
		_, _ = io.WriteString(writer, responses[0])
		responses = responses[1:]
	}))
	t.Cleanup(server.Close)
	return server
}
