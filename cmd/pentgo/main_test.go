package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/config"
)

func isolateConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	return root
}

func TestRunREPLInitializesWorkspaceAndSession(t *testing.T) {
	isolateConfig(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	var stdout, stderr bytes.Buffer

	code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	root := filepath.Join(workspace, ".pentgo")
	for _, name := range []string{"pentgo.db", "tmp"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("second exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	store, err := storage.OpenProjectStore(root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.LoadProject()
	if err != nil || len(project.Sessions) != 2 {
		t.Fatalf("project/err = %#v/%v", project, err)
	}
	if _, err := store.LoadSession(project.Sessions[0].ID); err != nil {
		t.Fatalf("session row: %v", err)
	}
}

func TestRunREPLOpensProjectFromCurrentDirectory(t *testing.T) {
	isolateConfig(t)
	parent := t.TempDir()
	created, err := storage.CreateProjectStore(parent, "fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(created.Root())
	var stdout, stderr bytes.Buffer
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	project, err := created.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "PentGo  "+project.Name) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunREPLDoesNotSelectSiblingProject(t *testing.T) {
	isolateConfig(t)
	parent := t.TempDir()
	created, err := storage.CreateProjectStore(parent, "fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	t.Chdir(parent)
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	project, err := created.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "project opened: "+project.ID) {
		t.Fatalf("sibling project was opened: %q", stdout.String())
	}
}

func TestRunREPLCreatesSessionsOnlyInCurrentDirectory(t *testing.T) {
	isolateConfig(t)
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	for _, directory := range []string{first, second} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	t.Chdir(first)
	if code := runREPL(context.Background(), strings.NewReader("/new\n/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("first exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	firstStore, err := storage.OpenProjectStore(filepath.Join(first, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	firstProject, err := firstStore.LoadProject()
	if closeErr := firstStore.Close(); err == nil {
		err = closeErr
	}
	if err != nil || len(firstProject.Sessions) != 2 {
		t.Fatalf("first project/err = %#v/%v", firstProject, err)
	}

	stdout.Reset()
	stderr.Reset()
	t.Chdir(second)
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("second exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	secondStore, err := storage.OpenProjectStore(filepath.Join(second, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	secondProject, err := secondStore.LoadProject()
	if closeErr := secondStore.Close(); err == nil {
		err = closeErr
	}
	if err != nil || len(secondProject.Sessions) != 1 {
		t.Fatalf("second project/err = %#v/%v", secondProject, err)
	}
}

func TestRunREPLMalformedConfigWarnsAndUsesDefaults(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())
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
	code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "config") || !strings.Contains(stderr.String(), "using defaults") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCommandResumeRequiresExistingWorkspace(t *testing.T) {
	isolateConfig(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	var stdout, stderr bytes.Buffer
	if code := runCommand(context.Background(), []string{"resume"}, strings.NewReader("/quit\n"), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, ".pentgo")); !os.IsNotExist(err) {
		t.Fatalf("resume created workspace: %v", err)
	}
}

func TestRunCommandResumeReusesExistingSession(t *testing.T) {
	isolateConfig(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	var stdout, stderr bytes.Buffer
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("startup exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCommand(context.Background(), []string{"resume"}, strings.NewReader("1\n/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("resume exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	store, err := storage.OpenProjectStore(filepath.Join(workspace, ".pentgo"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.LoadProject()
	if err != nil || len(project.Sessions) != 1 {
		t.Fatalf("project/err = %#v/%v", project, err)
	}
}

func TestRunCommandResumeSelectsRequestedHistory(t *testing.T) {
	isolateConfig(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	var stdout, stderr bytes.Buffer
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("first session exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("second session exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCommand(context.Background(), []string{"resume"}, strings.NewReader("2\n/quit\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("resume exit code = %d; stdout/stderr = %q/%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "恢复会话") || !strings.Contains(stdout.String(), "选择会话") {
		t.Fatalf("resume output = %q", stdout.String())
	}
}

func TestParseCommandAcceptsResumeOnly(t *testing.T) {
	command, err := parseCommand([]string{"resume"})
	if err != nil || !command.resume {
		t.Fatalf("command/err = %#v/%v", command, err)
	}
	for _, arguments := range [][]string{{"new"}, {"delete", "session-id"}, {"resume", "session-id"}} {
		if _, err := parseCommand(arguments); err == nil {
			t.Fatalf("parseCommand(%q) succeeded", arguments)
		}
	}
}
