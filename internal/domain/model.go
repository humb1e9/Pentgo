package domain

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
	Target       string    `json:"target"`
	Targets      []string  `json:"targets,omitempty"`
	Intent       string    `json:"intent"`
	Turns        int       `json:"turns"`
	ActiveTurnID string    `json:"active_turn_id,omitempty"`
	ActiveTurn   *Turn     `json:"active_turn,omitempty"`
	FinalSummary string    `json:"final_summary,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SessionSummary 是项目级、可由会话表重新构建的索引条目。
type SessionSummary struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Project 聚合会话，并记录项目级标识和时间信息。
type Project struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Sessions  []SessionSummary `json:"sessions,omitempty"`
}

// Fact 是同一项目内所有会话共享的键值观察结果。
type Fact struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Source    string    `json:"source,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	At        time.Time `json:"at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Blackboard 是项目范围内共享事实的集合。
type Blackboard struct {
	Facts []Fact `json:"facts"`
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
	return &Session{ID: strings.TrimSpace(id), Name: strings.TrimSpace(id), Intent: intent, StartedAt: startedAt, UpdatedAt: startedAt, Targets: []string{}}
}

// Rename 更新用户可见的会话名称及修改时间。
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

// AddTargets 追加去重、规范化后的目标，并将首个目标保留在兼容旧数据的 Target 字段中。
// 返回值表示会话状态是否发生变化。
func (session *Session) AddTargets(targets ...string) bool {
	if session == nil {
		return false
	}
	seen := make(map[string]bool, len(session.Targets)+1)
	for _, target := range session.Targets {
		seen[target] = true
	}
	if session.Target != "" {
		seen[session.Target] = true
	}
	changed := false
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		if session.Target == "" {
			session.Target = target
		}
		session.Targets = append(session.Targets, target)
		seen[target] = true
		changed = true
	}
	return changed
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
	session.ActiveTurnID = turn.ID
	session.UpdatedAt = startedAt
	return cloneTurn(turn), nil
}

// FinishTurn 将当前 turn 标记为完成，并记录最终摘要。
func (session *Session) FinishTurn(turnID, summary string, finishedAt time.Time) error {
	return session.finishTurn(turnID, TurnDone, "", summary, finishedAt)
}

// InterruptTurn 中断当前 turn，同时保留会话以便后续继续。
func (session *Session) InterruptTurn(turnID, reason string, finishedAt time.Time) error {
	return session.finishTurn(turnID, TurnInterrupted, reason, "", finishedAt)
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
	session.ActiveTurnID = ""
	if status == TurnDone {
		session.Turns++
		session.FinalSummary = strings.TrimSpace(summary)
	}
	session.UpdatedAt = finishedAt
	return nil
}

// NoteFact 按 key 新增或替换事实，使最新观察结果可供项目内所有会话使用。
func (board *Blackboard) NoteFact(fact Fact) error {
	if board == nil {
		return fmt.Errorf("blackboard is nil")
	}
	fact.Key = strings.TrimSpace(fact.Key)
	fact.Value = strings.TrimSpace(fact.Value)
	if fact.Key == "" || fact.Value == "" {
		return fmt.Errorf("fact key and value are required")
	}
	if fact.At.IsZero() {
		fact.At = time.Now().UTC()
	} else {
		fact.At = fact.At.UTC()
	}
	if fact.UpdatedAt.IsZero() {
		fact.UpdatedAt = time.Now().UTC()
	} else {
		fact.UpdatedAt = fact.UpdatedAt.UTC()
	}
	for index, existing := range board.Facts {
		if existing.Key == fact.Key {
			// A replacement is a fresh observation even when callers provide an
			// older source timestamp for the observation itself.
			fact.UpdatedAt = time.Now().UTC()
			board.Facts[index] = fact
			return nil
		}
	}
	board.Facts = append(board.Facts, fact)
	return nil
}

// CloneSession 深拷贝可变切片和当前 turn，供并发读取使用。
func CloneSession(source *Session) *Session {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Targets = append([]string(nil), source.Targets...)
	cloned.ActiveTurn = cloneTurn(source.ActiveTurn)
	return &cloned
}

// CloneProject 复制可重新构建的会话摘要索引。
func CloneProject(source *Project) *Project {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Sessions = append([]SessionSummary(nil), source.Sessions...)
	return &cloned
}

// CloneBlackboard 深拷贝项目共享事实。
func CloneBlackboard(source *Blackboard) *Blackboard {
	if source == nil {
		return nil
	}
	return &Blackboard{Facts: append([]Fact(nil), source.Facts...)}
}

// newID 创建不透明 ID，避免领域状态依赖具体存储后端。
func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-unknown"
	}
	return prefix + "-" + hex.EncodeToString(value)
}

// cloneTurn 复制 turn 中可选的完成时间戳。
func cloneTurn(turn *Turn) *Turn {
	if turn == nil {
		return nil
	}
	cloned := *turn
	cloned.FinishedAt = cloneTime(turn.FinishedAt)
	return &cloned
}

// cloneTime 防止快照调用方修改领域对象持有的时间戳。
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
