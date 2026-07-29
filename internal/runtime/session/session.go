package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type SessionStatus string

const (
	SessionPending   SessionStatus = "pending"
	SessionRunning   SessionStatus = "running"
	SessionDone      SessionStatus = "done"
	SessionFailed    SessionStatus = "failed"
	SessionCancelled SessionStatus = "cancelled"
)

type Finding struct {
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Description    string `json:"description"`
	EvidenceRefs   []int  `json:"evidence_refs"`
	Recommendation string `json:"recommendation"`
}

type AgentSession struct {
	ID           string        `json:"id"`
	Target       string        `json:"target"`
	Intent       string        `json:"intent"`
	Status       SessionStatus `json:"status"`
	StopReason   string        `json:"stop_reason,omitempty"`
	Turns        int           `json:"turns"`
	StartedAt    time.Time     `json:"started_at"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	Findings     []Finding     `json:"findings"`
	FinalSummary string        `json:"final_summary"`
}

func NewSession(target Target, intent string, startedAt time.Time) *AgentSession {
	return &AgentSession{ID: newSessionID(), Target: target.Canonical, Intent: intent, Status: SessionPending, StartedAt: startedAt, Findings: make([]Finding, 0)}
}

func (session *AgentSession) Start(_ time.Time) error {
	if session == nil || session.Status != SessionPending {
		return fmt.Errorf("session must be pending")
	}
	session.Status = SessionRunning
	return nil
}

func (session *AgentSession) Complete(reason string, finishedAt time.Time) error {
	return session.finish(SessionDone, reason, finishedAt)
}
func (session *AgentSession) Fail(reason string, finishedAt time.Time) error {
	return session.finish(SessionFailed, reason, finishedAt)
}
func (session *AgentSession) Cancel(reason string, finishedAt time.Time) error {
	return session.finish(SessionCancelled, reason, finishedAt)
}

func (session *AgentSession) finish(status SessionStatus, reason string, finishedAt time.Time) error {
	if session == nil || session.Status != SessionRunning {
		return fmt.Errorf("session must be running")
	}
	session.Status, session.StopReason = status, reason
	finishedAt = finishedAt.UTC()
	session.FinishedAt = &finishedAt
	return nil
}

func newSessionID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "session-unknown"
	}
	return "session-" + hex.EncodeToString(value)
}
