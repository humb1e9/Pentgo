package report

import (
	"os"
	"path/filepath"
	sess "pentgo/internal/runtime/session"
	"strings"
	"testing"
	"time"
)

func artifactSession(t *testing.T) *sess.AgentSession {
	t.Helper()
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	session := sess.NewSession(sess.Target{Canonical: "TARGET"}, "TASK", started)
	session.ID = "eng-ID"
	if err := session.Start(started); err != nil {
		t.Fatal(err)
	}
	if err := session.Complete("agent_finished", started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return session
}
func TestEngagementWriterPublishesJournalSessionReportAndWork(t *testing.T) {
	session := artifactSession(t)
	session.Findings = []sess.Finding{{Title: "Fixture finding", Severity: "high", Description: "Description.", EvidenceRefs: []int{1, 2}, Recommendation: "Recommendation."}}
	session.FinalSummary = "Agent summary."
	writer, err := NewEngagementWriter(t.TempDir(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	if err := os.WriteFile(writer.EvidencePath(), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.WorkDir(), "artifact.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts, err := writer.Publish(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{artifacts.EvidenceJSONL, artifacts.SessionJSON, artifacts.Markdown, filepath.Join(artifacts.WorkDirectory, "artifact.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(data), "### [HIGH] Fixture finding") >= strings.Index(string(data), "## Agent Summary") {
		t.Fatalf("report order = %s", data)
	}
}
func TestReportStatesWhenNoFindingsWereRecorded(t *testing.T) {
	if !strings.Contains(renderMarkdown(artifactSession(t)), "No findings were recorded.") {
		t.Fatal("missing no findings text")
	}
}
