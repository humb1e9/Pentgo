package storage

import (
	"fmt"

	sessionstate "pentgo/internal/session"
)

// OpenConversation opens an independent SQLite connection and loads the full
// ordered message history for an existing session.
func (store *ProjectStore) OpenConversation(id string) (*sessionstate.ConversationStore, error) {
	if store == nil || store.db == nil || !validID(id) {
		return nil, fmt.Errorf("invalid conversation session id")
	}
	db, err := OpenSQLite(store.DatabasePath())
	if err != nil {
		return nil, err
	}
	messages, err := sessionstate.LoadConversation(db, id)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return sessionstate.NewConversationStore(db, store.DatabasePath(), id, messages), nil
}
