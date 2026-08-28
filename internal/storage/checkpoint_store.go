package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
)

// SQLiteCheckpointStore persists opaque runner state without changing the
// authoritative Conversation, Evidence, Session, or Context Surface records.
type SQLiteCheckpointStore struct {
	db        *sql.DB
	sessionID string
	turnID    string
	now       func() time.Time
}

// NewSQLiteCheckpointStore opens a session- and turn-scoped checkpoint store.
func NewSQLiteCheckpointStore(databasePath, sessionID, turnID string) (*SQLiteCheckpointStore, error) {
	databasePath = strings.TrimSpace(databasePath)
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	if databasePath == "" || sessionID == "" || turnID == "" {
		return nil, fmt.Errorf("runner checkpoint scope is incomplete")
	}
	db, err := OpenSQLite(databasePath)
	if err != nil {
		return nil, fmt.Errorf("open runner checkpoint database: %w", err)
	}
	return &SQLiteCheckpointStore{db: db, sessionID: sessionID, turnID: turnID, now: time.Now}, nil
}

func (store *SQLiteCheckpointStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	if err := store.validate(checkpointID); err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var payload []byte
	err := store.db.QueryRowContext(ctx, `SELECT payload FROM runner_checkpoints WHERE session_id = ? AND turn_id = ? AND checkpoint_id = ?`, store.sessionID, store.turnID, checkpointID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load runner checkpoint: %w", err)
	}
	return append([]byte(nil), payload...), true, nil
}

func (store *SQLiteCheckpointStore) Set(ctx context.Context, checkpointID string, payload []byte) error {
	if err := store.validate(checkpointID); err != nil {
		return err
	}
	if payload == nil {
		return fmt.Errorf("runner checkpoint payload is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO runner_checkpoints(session_id, turn_id, checkpoint_id, payload, updated_at) VALUES(?, ?, ?, ?, ?) ON CONFLICT(session_id, turn_id, checkpoint_id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`, store.sessionID, store.turnID, checkpointID, payload, store.now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("save runner checkpoint: %w", err)
	}
	return nil
}

func (store *SQLiteCheckpointStore) Delete(ctx context.Context, checkpointID string) error {
	if err := store.validate(checkpointID); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := store.db.ExecContext(ctx, `DELETE FROM runner_checkpoints WHERE session_id = ? AND turn_id = ? AND checkpoint_id = ?`, store.sessionID, store.turnID, checkpointID)
	if err != nil {
		return fmt.Errorf("delete runner checkpoint: %w", err)
	}
	return nil
}

func (store *SQLiteCheckpointStore) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *SQLiteCheckpointStore) validate(checkpointID string) error {
	if store == nil || store.db == nil || store.sessionID == "" || store.turnID == "" || strings.TrimSpace(checkpointID) == "" {
		return fmt.Errorf("runner checkpoint scope is incomplete")
	}
	return nil
}

var _ compose.CheckPointStore = (*SQLiteCheckpointStore)(nil)
