package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/agent"
)

var (
	// ErrStaleSurfaceGeneration prevents an obsolete compactor snapshot from
	// replacing a newer projection.
	ErrStaleSurfaceGeneration = errors.New("context surface generation is stale")
	// ErrInvalidSurfaceRange means a replacement does not exactly cover a
	// contiguous sequence of existing Context Surface nodes.
	ErrInvalidSurfaceRange = errors.New("context surface replacement range is invalid")
)

// ContextSurfaceStore owns one session's persistent Context Surface connection.
type ContextSurfaceStore struct {
	mu        sync.Mutex
	db        *sql.DB
	path      string
	sessionID string
	closed    bool
	failed    error
}

// OpenContextSurface opens a session's persistent projection. It seeds source
// nodes only once, so transcript messages remain the immutable audit ledger.
func (store *ProjectStore) OpenContextSurface(sessionID string) (*ContextSurfaceStore, error) {
	if store == nil || store.db == nil || !validID(sessionID) {
		return nil, fmt.Errorf("invalid context surface session id")
	}
	db, err := openSQLite(store.DatabasePath())
	if err != nil {
		return nil, err
	}
	surface := &ContextSurfaceStore{db: db, path: store.DatabasePath(), sessionID: sessionID}
	if err := surface.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return surface, nil
}

// Path returns the SQLite file containing this surface.
func (store *ContextSurfaceStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Snapshot returns an ordered, defensive surface snapshot. Newly appended raw
// transcript messages are represented as source nodes before the snapshot.
func (store *ContextSurfaceStore) Snapshot() (agent.ContextSurface, error) {
	if store == nil || store.db == nil {
		return agent.ContextSurface{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return agent.ContextSurface{}, err
	}
	if err := store.syncSourceNodes(); err != nil {
		return agent.ContextSurface{}, err
	}
	return loadContextSurface(store.db, store.sessionID)
}

// StartCompaction durably records a planned replacement before a summarizer is
// called. It does not alter projection nodes.
func (store *ContextSurfaceStore) StartCompaction(generation int64, startSeq, endSeq int) (agent.CompactionLifecycle, error) {
	if store == nil || store.db == nil {
		return agent.CompactionLifecycle{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return agent.CompactionLifecycle{}, err
	}
	if startSeq <= 0 || endSeq < startSeq {
		return agent.CompactionLifecycle{}, ErrInvalidSurfaceRange
	}
	if err := store.syncSourceNodes(); err != nil {
		return agent.CompactionLifecycle{}, err
	}
	current, err := currentSurfaceGeneration(store.db, store.sessionID)
	if err != nil {
		return agent.CompactionLifecycle{}, err
	}
	if current != generation {
		return agent.CompactionLifecycle{}, ErrStaleSurfaceGeneration
	}
	lifecycle := agent.CompactionLifecycle{
		ID:         newSurfaceID(),
		SessionID:  store.sessionID,
		Generation: generation,
		StartSeq:   startSeq,
		EndSeq:     endSeq,
		Status:     agent.CompactionStarted,
	}
	if _, err := store.db.Exec(`
INSERT INTO context_compactions(id, session_id, generation, source_start_seq, source_end_seq, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, lifecycle.ID, lifecycle.SessionID, lifecycle.Generation, lifecycle.StartSeq, lifecycle.EndSeq, lifecycle.Status, timeValue(time.Now().UTC())); err != nil {
		return agent.CompactionLifecycle{}, fmt.Errorf("start context compaction: %w", err)
	}
	return lifecycle, nil
}

// ReplaceRange atomically validates a snapshot generation and source range,
// writes one replacement node, reorders positions, and commits its lifecycle.
func (store *ContextSurfaceStore) ReplaceRange(expectedGeneration int64, startSeq, endSeq int, replacement agent.SurfaceNode) (agent.ContextSurface, error) {
	if store == nil || store.db == nil {
		return agent.ContextSurface{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return agent.ContextSurface{}, err
	}
	if startSeq <= 0 || endSeq < startSeq || replacement.Kind == agent.SurfaceNodeSource || replacement.SourceStartSeq != startSeq || replacement.SourceEndSeq != endSeq || strings.TrimSpace(replacement.Content) == "" {
		return agent.ContextSurface{}, ErrInvalidSurfaceRange
	}
	if err := store.syncSourceNodes(); err != nil {
		return agent.ContextSurface{}, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return agent.ContextSurface{}, fmt.Errorf("begin context surface replacement: %w", err)
	}
	rollback := func(cause error) (agent.ContextSurface, error) {
		_ = tx.Rollback()
		return agent.ContextSurface{}, cause
	}
	generation, err := currentSurfaceGenerationTx(tx, store.sessionID)
	if err != nil {
		return rollback(err)
	}
	if generation != expectedGeneration {
		return rollback(ErrStaleSurfaceGeneration)
	}
	nodes, err := loadContextSurfaceNodesTx(tx, store.sessionID)
	if err != nil {
		return rollback(err)
	}
	first, last := selectedSurfaceRange(nodes, startSeq, endSeq)
	if first < 0 || last < first {
		return rollback(ErrInvalidSurfaceRange)
	}
	nextGeneration := generation + 1
	if replacement.ID == "" {
		replacement.ID = newSurfaceID()
	}
	replacement.Position = first
	replacement.Generation = nextGeneration
	if _, err := tx.Exec(`DELETE FROM context_surface_nodes WHERE session_id = ? AND position >= ? AND position <= ?`, store.sessionID, first, last); err != nil {
		return rollback(fmt.Errorf("delete context surface range: %w", err))
	}
	if _, err := tx.Exec(`
INSERT INTO context_surface_nodes(session_id, id, position, kind, source_start_seq, source_end_seq, content, generation)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.sessionID, replacement.ID, replacement.Position, replacement.Kind, replacement.SourceStartSeq, replacement.SourceEndSeq, replacement.Content, replacement.Generation); err != nil {
		return rollback(fmt.Errorf("insert context surface replacement: %w", err))
	}
	if err := renumberContextSurfaceNodesTx(tx, store.sessionID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`UPDATE context_surface_state SET generation = ? WHERE session_id = ?`, nextGeneration, store.sessionID); err != nil {
		return rollback(fmt.Errorf("advance context surface generation: %w", err))
	}
	if _, err := tx.Exec(`
UPDATE context_compactions
SET status = ?, error = '', finished_at = ?
WHERE session_id = ? AND generation = ? AND source_start_seq = ? AND source_end_seq = ? AND status = ?`, agent.CompactionCommitted, timeValue(time.Now().UTC()), store.sessionID, expectedGeneration, startSeq, endSeq, agent.CompactionStarted); err != nil {
		return rollback(fmt.Errorf("commit context compaction lifecycle: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return agent.ContextSurface{}, fmt.Errorf("commit context surface replacement: %w", err)
	}
	return loadContextSurface(store.db, store.sessionID)
}

// FailCompaction records a failed lifecycle but never changes surface nodes.
func (store *ContextSurfaceStore) FailCompaction(id string, failure error) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return err
	}
	message := "context compaction failed"
	if failure != nil {
		message = failure.Error()
	}
	result, err := store.db.Exec(`
UPDATE context_compactions
SET status = ?, error = ?, finished_at = ?
WHERE id = ? AND session_id = ? AND status = ?`, agent.CompactionFailed, message, timeValue(time.Now().UTC()), id, store.sessionID, agent.CompactionStarted)
	if err != nil {
		return fmt.Errorf("fail context compaction: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("fail context compaction rows: %w", err)
	} else if changed != 1 {
		return fmt.Errorf("context compaction %q is not started", id)
	}
	return nil
}

// Compactions returns lifecycle records for recovery and observability.
func (store *ContextSurfaceStore) Compactions() ([]agent.CompactionLifecycle, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := store.db.Query(`
SELECT id, generation, source_start_seq, source_end_seq, status, error
FROM context_compactions WHERE session_id = ? ORDER BY created_at, id`, store.sessionID)
	if err != nil {
		return nil, fmt.Errorf("read context compactions: %w", err)
	}
	defer rows.Close()
	var result []agent.CompactionLifecycle
	for rows.Next() {
		var lifecycle agent.CompactionLifecycle
		lifecycle.SessionID = store.sessionID
		if err := rows.Scan(&lifecycle.ID, &lifecycle.Generation, &lifecycle.StartSeq, &lifecycle.EndSeq, &lifecycle.Status, &lifecycle.Error); err != nil {
			return nil, fmt.Errorf("scan context compaction: %w", err)
		}
		result = append(result, lifecycle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read context compactions: %w", err)
	}
	return result, nil
}

// Close releases the store's private SQLite connection.
func (store *ContextSurfaceStore) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return store.failed
	}
	store.closed = true
	if err := store.db.Close(); err != nil && store.failed == nil {
		store.failed = fmt.Errorf("close context surface: %w", err)
	}
	return store.failed
}

func (store *ContextSurfaceStore) initialize() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	var exists bool
	if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)`, store.sessionID).Scan(&exists); err != nil {
		return fmt.Errorf("check context surface session: %w", err)
	}
	// NewSession opens its runtime resources before committing the session row.
	// Defer the first seed until the session is durable, while allowing that
	// resource lifecycle to remain unchanged.
	if !exists {
		return nil
	}
	if err := store.recoverUnfinishedCompactions(); err != nil {
		return err
	}
	return store.syncSourceNodes()
}

func (store *ContextSurfaceStore) ensureOpen() error {
	if store.failed != nil {
		return store.failed
	}
	if store.closed {
		return fmt.Errorf("context surface store is closed")
	}
	return nil
}

func (store *ContextSurfaceStore) recoverUnfinishedCompactions() error {
	_, err := store.db.Exec(`
UPDATE context_compactions
SET status = ?, error = 'interrupted before replacement commit', finished_at = ?
WHERE session_id = ? AND status = ?`, agent.CompactionFailed, timeValue(time.Now().UTC()), store.sessionID, agent.CompactionStarted)
	if err != nil {
		return fmt.Errorf("recover unfinished context compactions: %w", err)
	}
	return nil
}

func (store *ContextSurfaceStore) syncSourceNodes() error {
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin context surface sync: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO context_surface_state(session_id, generation) VALUES (?, 0)`, store.sessionID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("initialize context surface state: %w", err)
	}
	var nodeCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM context_surface_nodes WHERE session_id = ?`, store.sessionID).Scan(&nodeCount); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("count context surface nodes: %w", err)
	}
	if nodeCount == 0 {
		rows, err := tx.Query(`SELECT seq FROM transcript_messages WHERE session_id = ? ORDER BY seq`, store.sessionID)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read transcript source sequences: %w", err)
		}
		position := 0
		for rows.Next() {
			var sequence int
			if err := rows.Scan(&sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("scan transcript source sequence: %w", err)
			}
			if _, err := tx.Exec(`
INSERT INTO context_surface_nodes(session_id, id, position, kind, source_start_seq, source_end_seq, content, generation)
VALUES (?, ?, ?, ?, ?, ?, '', 0)`, store.sessionID, newSurfaceID(), position, agent.SurfaceNodeSource, sequence, sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("seed context surface source node: %w", err)
			}
			position++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("read transcript source sequences: %w", err)
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("close transcript source sequences: %w", err)
		}
	} else {
		var maxCovered sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(source_end_seq) FROM context_surface_nodes WHERE session_id = ?`, store.sessionID).Scan(&maxCovered); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read context surface coverage: %w", err)
		}
		position, err := nextSurfacePositionTx(tx, store.sessionID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		rows, err := tx.Query(`SELECT seq FROM transcript_messages WHERE session_id = ? AND seq > ? ORDER BY seq`, store.sessionID, maxCovered.Int64)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read new transcript source sequences: %w", err)
		}
		for rows.Next() {
			var sequence int
			if err := rows.Scan(&sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("scan new transcript source sequence: %w", err)
			}
			if _, err := tx.Exec(`
INSERT INTO context_surface_nodes(session_id, id, position, kind, source_start_seq, source_end_seq, content, generation)
VALUES (?, ?, ?, ?, ?, ?, '', 0)`, store.sessionID, newSurfaceID(), position, agent.SurfaceNodeSource, sequence, sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("append context surface source node: %w", err)
			}
			position++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("read new transcript source sequences: %w", err)
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("close new transcript source sequences: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit context surface sync: %w", err)
	}
	return nil
}

func loadContextSurface(db *sql.DB, sessionID string) (agent.ContextSurface, error) {
	generation, err := currentSurfaceGeneration(db, sessionID)
	if err != nil {
		return agent.ContextSurface{}, err
	}
	rows, err := db.Query(`
SELECT id, position, kind, source_start_seq, source_end_seq, content, generation
FROM context_surface_nodes WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return agent.ContextSurface{}, fmt.Errorf("read context surface nodes: %w", err)
	}
	defer rows.Close()
	surface := agent.ContextSurface{SessionID: sessionID, Generation: generation}
	for rows.Next() {
		var node agent.SurfaceNode
		if err := rows.Scan(&node.ID, &node.Position, &node.Kind, &node.SourceStartSeq, &node.SourceEndSeq, &node.Content, &node.Generation); err != nil {
			return agent.ContextSurface{}, fmt.Errorf("scan context surface node: %w", err)
		}
		surface.Nodes = append(surface.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return agent.ContextSurface{}, fmt.Errorf("read context surface nodes: %w", err)
	}
	return surface, nil
}

func loadContextSurfaceNodesTx(tx *sql.Tx, sessionID string) ([]agent.SurfaceNode, error) {
	rows, err := tx.Query(`
SELECT id, position, kind, source_start_seq, source_end_seq, content, generation
FROM context_surface_nodes WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read context surface nodes: %w", err)
	}
	defer rows.Close()
	var nodes []agent.SurfaceNode
	for rows.Next() {
		var node agent.SurfaceNode
		if err := rows.Scan(&node.ID, &node.Position, &node.Kind, &node.SourceStartSeq, &node.SourceEndSeq, &node.Content, &node.Generation); err != nil {
			return nil, fmt.Errorf("scan context surface node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read context surface nodes: %w", err)
	}
	return nodes, nil
}

func currentSurfaceGeneration(db *sql.DB, sessionID string) (int64, error) {
	var generation int64
	if err := db.QueryRow(`SELECT generation FROM context_surface_state WHERE session_id = ?`, sessionID).Scan(&generation); err != nil {
		return 0, fmt.Errorf("read context surface generation: %w", err)
	}
	return generation, nil
}

func currentSurfaceGenerationTx(tx *sql.Tx, sessionID string) (int64, error) {
	var generation int64
	if err := tx.QueryRow(`SELECT generation FROM context_surface_state WHERE session_id = ?`, sessionID).Scan(&generation); err != nil {
		return 0, fmt.Errorf("read context surface generation: %w", err)
	}
	return generation, nil
}

func selectedSurfaceRange(nodes []agent.SurfaceNode, startSeq, endSeq int) (int, int) {
	first, last := -1, -1
	for index, node := range nodes {
		if node.SourceStartSeq == startSeq {
			first = index
		}
		if first >= 0 {
			if index > first && node.SourceStartSeq != nodes[index-1].SourceEndSeq+1 {
				return -1, -1
			}
			if node.SourceEndSeq == endSeq {
				last = index
				break
			}
			if node.SourceEndSeq > endSeq {
				return -1, -1
			}
		}
	}
	return first, last
}

func nextSurfacePositionTx(tx *sql.Tx, sessionID string) (int, error) {
	var position int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position), -1) + 1 FROM context_surface_nodes WHERE session_id = ?`, sessionID).Scan(&position); err != nil {
		return 0, fmt.Errorf("read next context surface position: %w", err)
	}
	return position, nil
}

func renumberContextSurfaceNodesTx(tx *sql.Tx, sessionID string) error {
	if _, err := tx.Exec(`UPDATE context_surface_nodes SET position = position + 1000000 WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("stage context surface positions: %w", err)
	}
	rows, err := tx.Query(`SELECT id FROM context_surface_nodes WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return fmt.Errorf("read staged context surface positions: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan staged context surface position: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read staged context surface positions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close staged context surface positions: %w", err)
	}
	for position, id := range ids {
		if _, err := tx.Exec(`UPDATE context_surface_nodes SET position = ? WHERE session_id = ? AND id = ?`, position, sessionID, id); err != nil {
			return fmt.Errorf("renumber context surface positions: %w", err)
		}
	}
	return nil
}

func newSurfaceID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("surface-%d", time.Now().UTC().UnixNano())
}
