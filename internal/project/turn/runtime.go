package turn

import (
	"context"

	"pentgo/internal/core"
	projectcontext "pentgo/internal/project/context"
	"pentgo/internal/project/session"
)

// Runtime is the project-owned state the turn loop needs. bootstrap supplies
// the concrete process composition without being imported by this package.
type Runtime interface {
	Conversation(string) *session.ConversationStore
	FactSnapshot(context.Context) (string, error)
	Tools(context.Context) ([]core.Tool, error)
	Evidence() *EvidenceStore
	Persist(*session.Session) error
	Publish(string)
	Emit(string, Event)
	ContextPreparer() projectcontext.ContextPreparer
}

type ToolBuilder func(context.Context, Runtime, *session.Session, []core.Tool) ([]core.Tool, error)
