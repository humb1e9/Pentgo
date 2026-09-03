package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// TurnStatus 描述会话中一次模型调用的生命周期。
type TurnStatus string

// 合法的 turn 生命周期状态。被中断的 turn 不会关闭会话，后续仍可复用。
const (
	TurnRunning     TurnStatus = "running"
	TurnDone        TurnStatus = "done"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
)

// Turn 记录会话中的一次用户请求及其终止结果。
type Turn struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	Message    string     `json:"message"`
	Status     TurnStatus `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Session 是单个对话的可变状态。应用 worker 独占其写入权；调用方只能获取克隆快照，
// 不应持有或修改该指针。
type Session struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Intent       string    `json:"intent"`
	Turns        int       `json:"turns"`
	ActiveTurn   *Turn     `json:"active_turn,omitempty"`
	FinalSummary string    `json:"final_summary,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NewSession 创建 open 状态的会话，并规范化可选的标识与时间。
func NewSession(id, intent string, startedAt time.Time) *Session {
	if strings.TrimSpace(id) == "" {
		id = newID("session")
	}
	intent = strings.TrimSpace(intent)
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	return &Session{ID: strings.TrimSpace(id), Name: sessionName(intent), Intent: intent, StartedAt: startedAt, UpdatedAt: startedAt}
}

// Rename 更新用户可见的会话名称及修改时间。
func sessionName(intent string) string {
	intent = strings.TrimSpace(intent)
	if intent == "" || intent == "新会话" {
		return "新会话"
	}
	runes := []rune(intent)
	if len(runes) > 24 {
		runes = runes[:24]
	}
	return string(runes)
}

func (session *Session) Rename(name string) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name is empty")
	}
	session.Name = name
	session.UpdatedAt = time.Now().UTC()
	return nil
}

// BeginTurn 启动会话中唯一允许执行的 turn。
func (session *Session) BeginTurn(id, message string, startedAt time.Time) (*Turn, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if session.ActiveTurn != nil && session.ActiveTurn.Status == TurnRunning {
		return nil, fmt.Errorf("session already has a running turn")
	}
	if strings.TrimSpace(id) == "" {
		id = newID("turn")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	turn := &Turn{ID: strings.TrimSpace(id), SessionID: session.ID, Message: strings.TrimSpace(message), Status: TurnRunning, StartedAt: startedAt}
	session.ActiveTurn = turn
	session.UpdatedAt = startedAt
	copy := *turn
	return &copy, nil
}

// FinishTurn 将当前 turn 标记为完成，并记录最终摘要。
func (session *Session) FinishTurn(turnID, summary string, finishedAt time.Time) error {
	return session.finishTurn(turnID, TurnDone, "", summary, finishedAt)
}

// InterruptTurn 中断当前 turn，同时保留会话以便后续继续。
func (session *Session) InterruptTurn(turnID, reason string, finishedAt time.Time) error {
	return session.finishTurn(turnID, TurnInterrupted, reason, "", finishedAt)
}

// ResumeTurn restores an interrupted turn to running so its owning worker can
// continue the same durable execution rather than create another user turn.
func (session *Session) ResumeTurn(turnID string) error {
	if session == nil || session.ActiveTurn == nil || session.ActiveTurn.ID != strings.TrimSpace(turnID) || session.ActiveTurn.Status != TurnInterrupted {
		return fmt.Errorf("turn is not interrupted")
	}
	session.ActiveTurn.Status = TurnRunning
	session.ActiveTurn.Error = ""
	session.ActiveTurn.FinishedAt = nil
	session.UpdatedAt = time.Now().UTC()
	return nil
}

// FailTurn 记录非取消导致的 turn 失败，同时保持会话为 open 状态。
func (session *Session) FailTurn(turnID, reason string, finishedAt time.Time) error {
	return session.finishTurn(turnID, TurnFailed, reason, "", finishedAt)
}

// finishTurn 集中处理终止状态迁移，确保所有 turn 的时间戳和会话计数遵循相同约束。
func (session *Session) finishTurn(turnID string, status TurnStatus, reason, summary string, finishedAt time.Time) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	if session.ActiveTurn == nil || session.ActiveTurn.Status != TurnRunning || session.ActiveTurn.ID != strings.TrimSpace(turnID) {
		return fmt.Errorf("turn is not running")
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	} else {
		finishedAt = finishedAt.UTC()
	}
	session.ActiveTurn.Status = status
	session.ActiveTurn.Error = strings.TrimSpace(reason)
	session.ActiveTurn.FinishedAt = &finishedAt
	if status == TurnDone {
		session.Turns++
		session.FinalSummary = strings.TrimSpace(summary)
	}
	session.UpdatedAt = finishedAt
	return nil
}

// CloneSession 深拷贝可变切片和当前 turn，供并发读取使用。
func CloneSession(source *Session) *Session {
	if source == nil {
		return nil
	}
	cloned := *source
	if source.ActiveTurn != nil {
		turn := *source.ActiveTurn
		if source.ActiveTurn.FinishedAt != nil {
			finishedAt := *source.ActiveTurn.FinishedAt
			turn.FinishedAt = &finishedAt
		}
		cloned.ActiveTurn = &turn
	}
	return &cloned
}

// newID 创建不透明 ID，避免领域状态依赖具体存储后端。
func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-unknown"
	}
	return prefix + "-" + hex.EncodeToString(value)
}
