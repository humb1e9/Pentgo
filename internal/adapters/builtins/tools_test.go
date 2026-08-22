package builtins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pentgo/internal/agent"
)

func TestNewToolsRegistersWorkspaceActionsWithSchemas(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := NewTools(workspace)
	want := []string{"ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute"}
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d", len(tools))
	}
	for index, name := range want {
		if tools[index].Name() != name {
			t.Fatalf("tool %d = %q, want %q", index, tools[index].Name(), name)
		}
		schema, ok := tools[index].(agent.ToolSchemaProvider)
		if !ok || schema.InputSchema()["type"] != "object" {
			t.Fatalf("tool %q schema = %#v", name, schema)
		}
	}
}

func TestWorkspaceToolsValidatePathsAndPerformFileOperations(t *testing.T) {
	root := t.TempDir()
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	tools := toolMap(NewTools(workspace))
	write, err := tools["write_file"].Invoke(context.Background(), map[string]any{"file_path": "notes/result.txt", "content": "first\nTARGET\nfirst\n"})
	if err != nil || !strings.Contains(write, "result.txt") {
		t.Fatalf("write = %q / %v", write, err)
	}
	read, err := tools["read_file"].Invoke(context.Background(), map[string]any{"file_path": "notes/result.txt", "offset": float64(2), "limit": float64(1)})
	if err != nil || read != "TARGET" {
		t.Fatalf("read = %q / %v", read, err)
	}
	if _, err := tools["edit_file"].Invoke(context.Background(), map[string]any{"file_path": "notes/result.txt", "old_string": "first", "new_string": "changed", "replace_all": true}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "notes", "result.txt"))
	if err != nil || string(contents) != "changed\nTARGET\nchanged\n" {
		t.Fatalf("contents = %q / %v", contents, err)
	}
	for _, invocation := range []struct {
		name string
		args map[string]any
	}{
		{name: "read_file", args: map[string]any{"file_path": "../outside"}},
		{name: "write_file", args: map[string]any{"file_path": "/tmp/outside", "content": "x"}},
		{name: "glob", args: map[string]any{"pattern": "**/*.txt", "path": "../outside"}},
		{name: "grep", args: map[string]any{"pattern": "TARGET", "path": "/tmp"}},
	} {
		if _, err := tools[invocation.name].Invoke(context.Background(), invocation.args); err == nil {
			t.Fatalf("%s accepted unsafe path", invocation.name)
		}
	}
}

func TestWorkspaceToolsListGlobGrepAndExecute(t *testing.T) {
	root := t.TempDir()
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "result.txt"), []byte("TARGET value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := toolMap(NewTools(workspace))
	for _, invocation := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "ls", args: map[string]any{"path": "."}, want: "notes"},
		{name: "glob", args: map[string]any{"pattern": "**/*.txt", "path": "."}, want: "result.txt"},
		{name: "grep", args: map[string]any{"pattern": "TARGET", "path": "."}, want: "TARGET value"},
		{name: "execute", args: map[string]any{"command": "pwd"}, want: root},
	} {
		output, err := tools[invocation.name].Invoke(context.Background(), invocation.args)
		if err != nil || !strings.Contains(output, invocation.want) {
			t.Fatalf("%s = %q / %v; want %q", invocation.name, output, err, invocation.want)
		}
	}
}

func TestWorkspaceToolsRejectInvalidArguments(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := toolMap(NewTools(workspace))
	for _, invocation := range []struct {
		name string
		args map[string]any
	}{
		{name: "read_file", args: map[string]any{}},
		{name: "write_file", args: map[string]any{"file_path": "a.txt"}},
		{name: "edit_file", args: map[string]any{"file_path": "a.txt", "old_string": "old"}},
		{name: "glob", args: map[string]any{}},
		{name: "grep", args: map[string]any{}},
		{name: "execute", args: map[string]any{"command": " "}},
	} {
		if _, err := tools[invocation.name].Invoke(context.Background(), invocation.args); err == nil {
			t.Fatalf("%s accepted invalid arguments", invocation.name)
		}
	}
}

func toolMap(tools []agent.Tool) map[string]agent.Tool {
	result := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		result[tool.Name()] = tool
	}
	return result
}
