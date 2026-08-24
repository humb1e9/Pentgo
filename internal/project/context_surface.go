package project

import (
	"fmt"

	projectcontext "pentgo/internal/project/context"
)

// OpenContextSurface opens a session's persistent projection. It seeds source
// nodes only once, so conversation messages remain the immutable audit ledger.
func (store *ProjectStore) OpenContextSurface(sessionID string) (*projectcontext.ContextSurfaceStore, error) {
	if store == nil || store.db == nil || !validID(sessionID) {
		return nil, fmt.Errorf("invalid context surface session id")
	}
	db, err := OpenSQLite(store.DatabasePath())
	if err != nil {
		return nil, err
	}
	surface := projectcontext.NewSurfaceStore(db, store.DatabasePath(), sessionID)
	if err := surface.Initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return surface, nil
}
