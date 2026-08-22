//go:build !windows

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalToolCancellationKillsDescendants(t *testing.T) {
	directory := t.TempDir()
	commandPath := filepath.Join(directory, "tree-tool")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nif [ \"$1\" = \"spawn\" ]; then (while :; do :; done) & echo $! > \"$2\"; while :; do :; done; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(directory, "child.pid")
	tool := &localTool{name: "tree-tool", command: commandPath, maximumOutputBytes: 1024}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := tool.Invoke(ctx, map[string]any{"args": []any{"spawn", pidPath}}); err == nil {
		t.Fatal("cancelled process succeeded")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	var pid int
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidPath)
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child pid was not recorded")
	}
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant process %d survived cancellation", pid)
}
