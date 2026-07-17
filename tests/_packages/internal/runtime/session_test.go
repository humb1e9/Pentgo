package runtime

import (
	"testing"
	"time"
)

func TestSessionTracksExplicitLifecycle(t *testing.T) {
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	session := NewSession(Target{Raw: "https://example.com", Canonical: "https://example.com"}, "检查目标", started)
	if session.ID == "" || session.Status != SessionPending || !session.StartedAt.Equal(started) {
		t.Fatalf("new session = %+v", session)
	}
	if err := session.Start(started.Add(time.Second)); err != nil || session.Status != SessionRunning {
		t.Fatalf("Start() = %v, session = %+v", err, session)
	}
	if err := session.Complete("task_complete", started.Add(2*time.Second)); err != nil || session.Status != SessionDone || session.StopReason != "task_complete" {
		t.Fatalf("Complete() = %v, session = %+v", err, session)
	}
	if session.FinishedAt == nil || !session.FinishedAt.Equal(started.Add(2*time.Second)) {
		t.Fatalf("finished at = %v", session.FinishedAt)
	}
}

func TestSessionRejectsInvalidLifecycleTransition(t *testing.T) {
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now())
	if err := session.Complete("task_complete", time.Now()); err == nil {
		t.Fatal("Complete() error = nil")
	}
}
