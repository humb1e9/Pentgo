package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"pentgo/internal/runtime/verify"
)

// SessionStatus 是单一终端任务的生命周期状态。
type SessionStatus string

const (
	SessionPending   SessionStatus = "pending"
	SessionRunning   SessionStatus = "running"
	SessionDone      SessionStatus = "done"
	SessionFailed    SessionStatus = "failed"
	SessionCancelled SessionStatus = "cancelled"
)

// AgentSession 保存终端 Agent 执行所需的独立领域状态。
type AgentSession struct {
	ID           string                      `json:"id"`
	Target       Target                      `json:"target"`
	Intent       string                      `json:"intent"`
	Status       SessionStatus               `json:"status"`
	StartedAt    time.Time                   `json:"started_at"`
	FinishedAt   *time.Time                  `json:"finished_at,omitempty"`
	StopReason   string                      `json:"stop_reason,omitempty"`
	Turn         int                         `json:"turn"`
	LoadedSkills []string                    `json:"loaded_skills,omitempty"`
	Findings     []verify.VerificationResult `json:"findings,omitempty"`
	Sessions     []SessionPublic             `json:"sessions,omitempty"`
	Timeline     []TimelineEvent             `json:"timeline,omitempty"`
}

// TimelineEvent 记录不会携带完整代码或原始输出的运行时事件。
type TimelineEvent struct {
	At     time.Time `json:"at"`
	Turn   int       `json:"turn,omitempty"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// NewSession 创建一个尚未开始执行的 AgentSession。
func NewSession(target Target, intent string, startedAt time.Time) *AgentSession {
	return &AgentSession{
		ID:        newSessionID(),
		Target:    target,
		Intent:    intent,
		Status:    SessionPending,
		StartedAt: startedAt,
	}
}

// Start 将会话从 pending 转为 running。
func (session *AgentSession) Start(_ time.Time) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}
	if session.Status != SessionPending {
		return fmt.Errorf("cannot start session in %s state", session.Status)
	}
	session.Status = SessionRunning
	return nil
}

// Complete 将运行中的会话以正常完成状态结束。
func (session *AgentSession) Complete(reason string, finishedAt time.Time) error {
	return session.finish(SessionDone, reason, finishedAt)
}

// Fail 将运行中的会话以失败状态结束。
func (session *AgentSession) Fail(reason string, finishedAt time.Time) error {
	return session.finish(SessionFailed, reason, finishedAt)
}

// Cancel 将运行中的会话以取消状态结束。
func (session *AgentSession) Cancel(reason string, finishedAt time.Time) error {
	return session.finish(SessionCancelled, reason, finishedAt)
}

// AddEvent 将运行时摘要写入会话时间线。
func (session *AgentSession) AddEvent(turn int, kind, detail string, at time.Time) {
	if session == nil {
		return
	}
	session.Timeline = append(session.Timeline, TimelineEvent{At: at, Turn: turn, Kind: kind, Detail: detail})
}

func (session *AgentSession) finish(status SessionStatus, reason string, finishedAt time.Time) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}
	if session.Status != SessionRunning {
		return fmt.Errorf("cannot finish session in %s state", session.Status)
	}
	session.Status = status
	session.StopReason = reason
	session.FinishedAt = &finishedAt
	return nil
}

func newSessionID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err == nil {
		return "eng-" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("eng-%d", time.Now().UTC().UnixNano())
}
