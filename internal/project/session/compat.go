package session

import (
	"context"
	"database/sql"
	"time"

	"pentgo/internal/core"
	sessionstate "pentgo/internal/session"
)

// Deprecated: use internal/session directly.
type TurnStatus = sessionstate.TurnStatus

type Turn = sessionstate.Turn
type Session = sessionstate.Session
type SessionSummary = sessionstate.SessionSummary
type Project = sessionstate.Project
type TurnFunc = sessionstate.TurnFunc
type Worker = sessionstate.Worker
type Event = sessionstate.Event
type ConversationStore = sessionstate.ConversationStore

const (
	TurnRunning     = sessionstate.TurnRunning
	TurnDone        = sessionstate.TurnDone
	TurnInterrupted = sessionstate.TurnInterrupted
	TurnFailed      = sessionstate.TurnFailed

	EventTurnStarted      = sessionstate.EventTurnStarted
	EventToolStarted      = sessionstate.EventToolStarted
	EventToolFinished     = sessionstate.EventToolFinished
	EventAssistantMessage = sessionstate.EventAssistantMessage
	EventAssistantDelta   = sessionstate.EventAssistantDelta
	EventTurnFinished     = sessionstate.EventTurnFinished
	EventTurnFailed       = sessionstate.EventTurnFailed
)

func NewSession(id, intent string, startedAt time.Time) *Session {
	return sessionstate.NewSession(id, intent, startedAt)
}

func CloneSession(source *Session) *Session { return sessionstate.CloneSession(source) }
func CloneProject(source *Project) *Project { return sessionstate.CloneProject(source) }
func ResumeTurnRequested(ctx context.Context) bool {
	return sessionstate.ResumeTurnRequested(ctx)
}
func NewWorker(parent context.Context, session *Session, turn TurnFunc) (*Worker, error) {
	return sessionstate.NewWorker(parent, session, turn)
}
func NewConversationStore(db *sql.DB, path, sessionID string, messages []core.Message) *ConversationStore {
	return sessionstate.NewConversationStore(db, path, sessionID, messages)
}
func LoadConversation(db *sql.DB, sessionID string) ([]core.Message, error) {
	return sessionstate.LoadConversation(db, sessionID)
}
func LoadConversationFrom(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, sessionID string) ([]core.Message, error) {
	return sessionstate.LoadConversationFrom(queryer, sessionID)
}
