package builtins

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

func TestWorkspaceOperatesWithinWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Write(context.Background(), &filesystem.WriteRequest{FilePath: "notes/result.txt", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	content, err := workspace.Read(context.Background(), &filesystem.ReadRequest{FilePath: "notes/result.txt"})
	if err != nil || content.Content != "first" {
		t.Fatalf("content/err = %#v/%v", content, err)
	}
	if _, err := workspace.Read(context.Background(), &filesystem.ReadRequest{FilePath: filepath.Join("..", filepath.Base(outside), "secret.txt")}); err == nil {
		t.Fatal("outside read succeeded")
	}
	if runtime.GOOS != "windows" {
		result, err := workspace.Execute(context.Background(), &filesystem.ExecuteRequest{Command: "pwd"})
		if err != nil || result == nil || !strings.Contains(result.Output, root) {
			t.Fatalf("execute result/err = %#v/%v", result, err)
		}
	}
}
