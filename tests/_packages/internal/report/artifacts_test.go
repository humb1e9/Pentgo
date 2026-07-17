package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/runtime"
)

func artifactTestSession(t *testing.T) *runtime.AgentSession {
	t.Helper()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	session := runtime.NewSession(runtime.Target{Raw: "https://example.test", Canonical: "https://example.test"}, "检查目标", started)
	session.ID = "eng-20260717-120000-test"
	if err := session.Start(started); err != nil {
		t.Fatal(err)
	}
	session.Turn = 2
	session.AddEvent(1, "execution", "1 block", started.Add(time.Second))
	if err := session.Complete("task_complete", started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestEngagementWriterPublishesRuntimeSessionReportEvidenceAndWorkDir(t *testing.T) {
	session := artifactTestSession(t)
	root := t.TempDir()
	writer, err := NewEngagementWriter(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	if err := os.WriteFile(filepath.Join(writer.WorkDir(), "turn-001-block-001.py"), []byte("print('evidence')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidencePath, err := writer.WriteEvidence("agent-turn-001-block-001", map[string]any{"status": "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := writer.Publish(session, time.Date(2026, 7, 17, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.WorkDirectory != filepath.Join(artifacts.Directory, "work") {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	for _, path := range []string{artifacts.SessionJSON, artifacts.Markdown, filepath.Join(artifacts.Directory, evidencePath), filepath.Join(artifacts.WorkDirectory, "turn-001-block-001.py")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(artifacts.SessionJSON)
	if err != nil {
		t.Fatal(err)
	}
	var decoded runtime.AgentSession
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != runtime.SessionDone || decoded.StopReason != "task_complete" {
		t.Fatalf("session = %+v", decoded)
	}
	reportBody, err := os.ReadFile(artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# PentGo Agent Report", "https://example.test", "task_complete", "execution", "1 block"} {
		if !strings.Contains(string(reportBody), want) {
			t.Fatalf("report missing %q:\n%s", want, reportBody)
		}
	}
}

func TestEngagementWriterRejectsInvalidAndExistingDestination(t *testing.T) {
	if _, err := NewEngagementWriter(t.TempDir(), "../escape"); err == nil {
		t.Fatal("invalid ID error = nil")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "eng-existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngagementWriter(root, "eng-existing"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing destination error = %v", err)
	}
}

func TestWriteArtifactsPublishesCancelledRuntimeSession(t *testing.T) {
	session := artifactTestSession(t)
	session.Status = runtime.SessionRunning
	if err := session.Cancel("operator_cancelled", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	artifacts, err := WriteArtifacts(session, t.TempDir(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(artifacts.SessionJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status": "cancelled"`) {
		t.Fatalf("session.json = %s", data)
	}
	if _, err := os.Stat(artifacts.Directory); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact directory missing: %v", err)
	}
}
