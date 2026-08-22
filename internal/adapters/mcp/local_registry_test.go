package mcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
)

func TestNewLocalRegistryRegistersConfiguredToolsInNameOrder(t *testing.T) {
	registry := mustLocalRegistry(t, config.LocalTools{
		"zeta":  {Command: "/not/checked/zeta", Description: "zeta description"},
		"alpha": {Command: "/not/checked/alpha"},
	}, 1024)
	tools, err := registry.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(tools); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("tools = %v", got)
	}
	if tools[0].Description() != "调用用户配置的本机 CLI alpha 执行原生参数。args 的每项都是一个独立命令行参数。" || tools[1].Description() != "zeta description" {
		t.Fatalf("descriptions = %q / %q", tools[0].Description(), tools[1].Description())
	}
	tools[0] = nil
	again, err := registry.Tools(context.Background())
	if err != nil || again[0] == nil {
		t.Fatalf("defensive copy = %#v / %v", again, err)
	}
}

func TestLocalToolSchemaUsesUniversalArgsArray(t *testing.T) {
	tool := localToolByName(t, mustLocalRegistry(t, config.LocalTools{"httpx": {Command: "httpx"}}, 1024), "httpx")
	want := map[string]any{
		"type":     "object",
		"required": []any{"args"},
		"properties": map[string]any{
			"args": map[string]any{
				"type":        "array",
				"description": "传给本机 CLI 的原生参数数组；每项必须是一个独立 argv 元素。",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
	if got := tool.InputSchema(); !reflect.DeepEqual(got, want) {
		t.Fatalf("schema = %#v", got)
	}
}

func TestLocalToolPassesArgumentsAsExactArgv(t *testing.T) {
	directory := t.TempDir()
	command := writeExecutable(t, directory, "tool", "#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n")
	registry := mustLocalRegistry(t, config.LocalTools{"custom-recon": {Command: command}}, 1024)
	tool := localToolByName(t, registry, "custom-recon")
	output, err := tool.Invoke(context.Background(), map[string]any{"args": []any{"one two", "$(not-shell)"}})
	if err != nil {
		t.Fatal(err)
	}
	if output != "<one two>\n<$(not-shell)>\n" {
		t.Fatalf("output = %q", output)
	}
}

func TestLocalToolRejectsInvalidArgumentsAndKeepsErrorOutput(t *testing.T) {
	directory := t.TempDir()
	command := writeExecutable(t, directory, "tool", "#!/bin/sh\necho command-error >&2\nexit 7\n")
	tool := localToolByName(t, mustLocalRegistry(t, config.LocalTools{"tool": {Command: command}}, 1024), "tool")
	if _, err := tool.Invoke(context.Background(), map[string]any{}); err == nil {
		t.Fatal("missing args succeeded")
	}
	if _, err := tool.Invoke(context.Background(), map[string]any{"args": []any{"ok", 42}}); err == nil {
		t.Fatal("non-string args succeeded")
	}
	output, err := tool.Invoke(context.Background(), map[string]any{"args": []any{"run"}})
	if err == nil || !strings.Contains(output, "command-error") {
		t.Fatalf("output/err = %q/%v", output, err)
	}
}

func TestLocalToolHonorsCancellationAndBoundsOutput(t *testing.T) {
	directory := t.TempDir()
	command := writeExecutable(t, directory, "tool", "#!/bin/sh\nif [ \"$1\" = \"sleep\" ]; then while :; do :; done; fi\nprintf '你好世界'\n")
	tool := localToolByName(t, mustLocalRegistry(t, config.LocalTools{"tool": {Command: command}}, 4), "tool")
	output, err := tool.Invoke(context.Background(), map[string]any{"args": []any{"output"}})
	if err != nil || output != "你\n[输出已截断]" {
		t.Fatalf("output/err = %q/%v", output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := tool.Invoke(ctx, map[string]any{"args": []any{"sleep"}}); err == nil {
		t.Fatal("cancelled command succeeded")
	}
}

func mustLocalRegistry(t *testing.T, configurations config.LocalTools, maximumOutputBytes int) *LocalRegistry {
	t.Helper()
	return NewLocalRegistry(configurations, maximumOutputBytes)
}

func localToolByName(t *testing.T, registry *LocalRegistry, name string) *localTool {
	t.Helper()
	for _, tool := range registry.tools {
		if tool.Name() == name {
			return tool.(*localTool)
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func toolNames(tools []agent.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func writeExecutable(t *testing.T, directory, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires POSIX")
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
