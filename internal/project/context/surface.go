package context

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"pentgo/internal/core"
	sessionstate "pentgo/internal/project/session"
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

// NewSurfaceStore constructs a surface store bound to an existing database
// connection. The root package uses it to open a session's projection.
func NewSurfaceStore(db *sql.DB, path, sessionID string) *ContextSurfaceStore {
	return &ContextSurfaceStore{db: db, path: path, sessionID: sessionID}
}

// Initialize seeds source nodes and recovers unfinished compactions for an
// existing session. The root package calls it after construction.
func (store *ContextSurfaceStore) Initialize() error {
	return store.initialize()
}

// Path returns the SQLite file containing this surface.
func (store *ContextSurfaceStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Snapshot returns an ordered, defensive surface snapshot. Newly appended raw
// conversation messages are represented as source nodes before the snapshot.
func (store *ContextSurfaceStore) Snapshot() (core.ContextSurface, error) {
	surface, _, err := store.SnapshotWithConversation()
	return surface, err
}

// SnapshotWithConversation reads the Context Surface and referenced immutable
// conversation rows through one SQLite transaction. A caller therefore never
// assembles a source node against a separately observed conversation revision.
func (store *ContextSurfaceStore) SnapshotWithConversation() (core.ContextSurface, []core.Message, error) {
	if store == nil || store.db == nil {
		return core.ContextSurface{}, nil, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return core.ContextSurface{}, nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := store.syncSourceNodes(); err != nil {
			return core.ContextSurface{}, nil, err
		}
		tx, err := store.db.Begin()
		if err != nil {
			return core.ContextSurface{}, nil, fmt.Errorf("begin context surface snapshot: %w", err)
		}
		rollback := func(cause error) (core.ContextSurface, []core.Message, error) {
			_ = tx.Rollback()
			return core.ContextSurface{}, nil, cause
		}
		generation, err := currentSurfaceGenerationTx(tx, store.sessionID)
		if err != nil {
			return rollback(err)
		}
		nodes, err := loadContextSurfaceNodesTx(tx, store.sessionID)
		if err != nil {
			return rollback(err)
		}
		messages, err := sessionstate.LoadConversationFrom(tx, store.sessionID)
		if err != nil {
			return rollback(err)
		}
		maxCovered := 0
		for _, node := range nodes {
			if node.SourceEndSeq > maxCovered {
				maxCovered = node.SourceEndSeq
			}
		}
		if len(messages) > maxCovered {
			_ = tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			return core.ContextSurface{}, nil, fmt.Errorf("commit context surface snapshot: %w", err)
		}
		return core.ContextSurface{SessionID: store.sessionID, Generation: generation, Nodes: nodes}, messages, nil
	}
	return core.ContextSurface{}, nil, fmt.Errorf("context surface snapshot changed during assembly")
}

// StartCompaction durably records a planned replacement before a summarizer is
// called. It does not alter projection nodes.
func (store *ContextSurfaceStore) StartCompaction(generation int64, startSeq, endSeq int) (core.CompactionLifecycle, error) {
	if store == nil || store.db == nil {
		return core.CompactionLifecycle{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return core.CompactionLifecycle{}, err
	}
	if startSeq <= 0 || endSeq < startSeq {
		return core.CompactionLifecycle{}, ErrInvalidSurfaceRange
	}
	if current, err := currentSurfaceGenerationOrZero(store.db, store.sessionID); err != nil {
		return core.CompactionLifecycle{}, err
	} else if current != generation {
		return core.CompactionLifecycle{}, ErrStaleSurfaceGeneration
	}
	if err := store.syncSourceNodes(); err != nil {
		return core.CompactionLifecycle{}, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return core.CompactionLifecycle{}, fmt.Errorf("begin context compaction: %w", err)
	}
	rollback := func(cause error) (core.CompactionLifecycle, error) {
		_ = tx.Rollback()
		return core.CompactionLifecycle{}, cause
	}
	current, err := currentSurfaceGenerationTx(tx, store.sessionID)
	if err != nil {
		return rollback(err)
	}
	if current != generation {
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
	if err := validateBalancedToolPairsTx(tx, store.sessionID, startSeq, endSeq); err != nil {
		return rollback(err)
	}
	lifecycle := core.CompactionLifecycle{
		ID:         newSurfaceID(),
		SessionID:  store.sessionID,
		Generation: generation,
		StartSeq:   startSeq,
		EndSeq:     endSeq,
		Status:     core.CompactionStarted,
	}
	if _, err := tx.Exec(`
INSERT INTO context_compactions(id, session_id, generation, source_start_seq, source_end_seq, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, lifecycle.ID, lifecycle.SessionID, lifecycle.Generation, lifecycle.StartSeq, lifecycle.EndSeq, lifecycle.Status, timeValue(time.Now().UTC())); err != nil {
		return rollback(fmt.Errorf("start context compaction: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return core.CompactionLifecycle{}, fmt.Errorf("commit context compaction start: %w", err)
	}
	return lifecycle, nil
}

// ReplaceRange atomically validates a snapshot generation and source range,
// writes one replacement node, reorders positions, and commits its lifecycle.
func (store *ContextSurfaceStore) ReplaceRange(expectedGeneration int64, startSeq, endSeq int, replacement core.SurfaceNode) (core.ContextSurface, error) {
	return store.ReplaceRangeWithPrunes(expectedGeneration, startSeq, endSeq, replacement, nil)
}

// ReplaceRangeWithPrunes commits a checkpoint replacement and any retained-tail
// tool-result prunes in one transaction. A failure leaves the prior Surface
// unchanged.
func (store *ContextSurfaceStore) ReplaceRangeWithPrunes(expectedGeneration int64, startSeq, endSeq int, replacement core.SurfaceNode, prunes map[int]string) (core.ContextSurface, error) {
	if store == nil || store.db == nil {
		return core.ContextSurface{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return core.ContextSurface{}, err
	}
	if startSeq <= 0 || endSeq < startSeq || replacement.Kind == core.SurfaceNodeSource || replacement.SourceStartSeq != startSeq || replacement.SourceEndSeq != endSeq || strings.TrimSpace(replacement.Content) == "" {
		return core.ContextSurface{}, ErrInvalidSurfaceRange
	}
	if current, err := currentSurfaceGenerationOrZero(store.db, store.sessionID); err != nil {
		return core.ContextSurface{}, err
	} else if current != expectedGeneration {
		return core.ContextSurface{}, ErrStaleSurfaceGeneration
	}
	if err := store.syncSourceNodes(); err != nil {
		return core.ContextSurface{}, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return core.ContextSurface{}, fmt.Errorf("begin context surface replacement: %w", err)
	}
	rollback := func(cause error) (core.ContextSurface, error) {
		_ = tx.Rollback()
		return core.ContextSurface{}, cause
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
	if err := validateBalancedToolPairsTx(tx, store.sessionID, startSeq, endSeq); err != nil {
		return rollback(err)
	}
	nextGeneration := generation + 1
	pruneSequences := make([]int, 0, len(prunes))
	for sourceSeq, content := range prunes {
		if sourceSeq <= endSeq || strings.TrimSpace(content) == "" {
			return rollback(ErrInvalidSurfaceRange)
		}
		pruneSequences = append(pruneSequences, sourceSeq)
	}
	sort.Ints(pruneSequences)
	pruneIDs := make(map[int]string, len(pruneSequences))
	for _, sourceSeq := range pruneSequences {
		var id string
		var kind core.SurfaceNodeKind
		if err := tx.QueryRow(`SELECT id, kind FROM context_surface_nodes WHERE session_id = ? AND source_start_seq = ? AND source_end_seq = ?`, store.sessionID, sourceSeq, sourceSeq).Scan(&id, &kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rollback(ErrInvalidSurfaceRange)
			}
			return rollback(fmt.Errorf("read checkpoint tail prune node: %w", err))
		}
		if kind != core.SurfaceNodeSource {
			return rollback(ErrInvalidSurfaceRange)
		}
		var role string
		if err := tx.QueryRow(`SELECT role FROM conversation_messages WHERE session_id = ? AND seq = ?`, store.sessionID, sourceSeq).Scan(&role); err != nil {
			return rollback(fmt.Errorf("read checkpoint tail prune source: %w", err))
		}
		if role != core.RoleTool {
			return rollback(ErrInvalidSurfaceRange)
		}
		pruneIDs[sourceSeq] = id
	}
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
	for _, sourceSeq := range pruneSequences {
		result, err := tx.Exec(`
UPDATE context_surface_nodes SET kind = ?, content = ?, generation = ?
WHERE session_id = ? AND id = ? AND kind = ?`, core.SurfaceNodePrunedTool, prunes[sourceSeq], nextGeneration, store.sessionID, pruneIDs[sourceSeq], core.SurfaceNodeSource)
		if err != nil {
			return rollback(fmt.Errorf("write checkpoint tail prune: %w", err))
		}
		if changed, err := result.RowsAffected(); err != nil {
			return rollback(fmt.Errorf("write checkpoint tail prune rows: %w", err))
		} else if changed != 1 {
			return rollback(ErrInvalidSurfaceRange)
		}
	}
	if err := renumberContextSurfaceNodesTx(tx, store.sessionID); err != nil {
		return rollback(err)
	}
	if _, err := tx.Exec(`UPDATE context_surface_state SET generation = ? WHERE session_id = ?`, nextGeneration, store.sessionID); err != nil {
		return rollback(fmt.Errorf("advance context surface generation: %w", err))
	}
	result, err := tx.Exec(`
UPDATE context_compactions
SET status = ?, error = '', finished_at = ?
WHERE session_id = ? AND generation = ? AND source_start_seq = ? AND source_end_seq = ? AND status = ?`, core.CompactionCommitted, timeValue(time.Now().UTC()), store.sessionID, expectedGeneration, startSeq, endSeq, core.CompactionStarted)
	if err != nil {
		return rollback(fmt.Errorf("commit context compaction lifecycle: %w", err))
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("commit context compaction lifecycle rows: %w", err))
	}
	if changed != 1 {
		return rollback(fmt.Errorf("context compaction lifecycle is missing or ambiguous"))
	}
	if err := tx.Commit(); err != nil {
		return core.ContextSurface{}, fmt.Errorf("commit context surface replacement: %w", err)
	}
	return loadContextSurface(store.db, store.sessionID)
}

// PruneTool replaces exactly one raw tool-result source node without altering
// its source range. Tool-call pairing remains visible through the untouched
// assistant node, so this operation does not create a checkpoint lifecycle.
func (store *ContextSurfaceStore) PruneTool(expectedGeneration int64, sourceSeq int, content string) (core.ContextSurface, error) {
	return store.PruneTools(expectedGeneration, map[int]string{sourceSeq: content})
}

// PruneTools atomically replaces raw tool-result nodes. Any stale generation,
// invalid node, or storage failure leaves all selected nodes unchanged.
func (store *ContextSurfaceStore) PruneTools(expectedGeneration int64, replacements map[int]string) (core.ContextSurface, error) {
	if store == nil || store.db == nil {
		return core.ContextSurface{}, fmt.Errorf("context surface store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.ensureOpen(); err != nil {
		return core.ContextSurface{}, err
	}
	if len(replacements) == 0 {
		return core.ContextSurface{}, ErrInvalidSurfaceRange
	}
	sequences := make([]int, 0, len(replacements))
	for sourceSeq, content := range replacements {
		if sourceSeq <= 0 || strings.TrimSpace(content) == "" {
			return core.ContextSurface{}, ErrInvalidSurfaceRange
		}
		sequences = append(sequences, sourceSeq)
	}
	sort.Ints(sequences)
	if current, err := currentSurfaceGenerationOrZero(store.db, store.sessionID); err != nil {
		return core.ContextSurface{}, err
	} else if current != expectedGeneration {
		return core.ContextSurface{}, ErrStaleSurfaceGeneration
	}
	if err := store.syncSourceNodes(); err != nil {
		return core.ContextSurface{}, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return core.ContextSurface{}, fmt.Errorf("begin context tool prune: %w", err)
	}
	rollback := func(cause error) (core.ContextSurface, error) {
		_ = tx.Rollback()
		return core.ContextSurface{}, cause
	}
	generation, err := currentSurfaceGenerationTx(tx, store.sessionID)
	if err != nil {
		return rollback(err)
	}
	if generation != expectedGeneration {
		return rollback(ErrStaleSurfaceGeneration)
	}
	nodeIDs := make(map[int]string, len(sequences))
	for _, sourceSeq := range sequences {
		var nodeID string
		var kind core.SurfaceNodeKind
		if err := tx.QueryRow(`
SELECT id, kind FROM context_surface_nodes
WHERE session_id = ? AND source_start_seq = ? AND source_end_seq = ?`, store.sessionID, sourceSeq, sourceSeq).Scan(&nodeID, &kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rollback(ErrInvalidSurfaceRange)
			}
			return rollback(fmt.Errorf("read context tool prune node: %w", err))
		}
		if kind != core.SurfaceNodeSource {
			return rollback(ErrInvalidSurfaceRange)
		}
		var role string
		if err := tx.QueryRow(`SELECT role FROM conversation_messages WHERE session_id = ? AND seq = ?`, store.sessionID, sourceSeq).Scan(&role); err != nil {
			return rollback(fmt.Errorf("read context tool prune source: %w", err))
		}
		if role != core.RoleTool {
			return rollback(ErrInvalidSurfaceRange)
		}
		nodeIDs[sourceSeq] = nodeID
	}
	nextGeneration := generation + 1
	for _, sourceSeq := range sequences {
		result, err := tx.Exec(`
UPDATE context_surface_nodes
SET kind = ?, content = ?, generation = ?
WHERE session_id = ? AND id = ? AND kind = ?`, core.SurfaceNodePrunedTool, replacements[sourceSeq], nextGeneration, store.sessionID, nodeIDs[sourceSeq], core.SurfaceNodeSource)
		if err != nil {
			return rollback(fmt.Errorf("write context tool prune: %w", err))
		}
		if changed, err := result.RowsAffected(); err != nil {
			return rollback(fmt.Errorf("write context tool prune rows: %w", err))
		} else if changed != 1 {
			return rollback(ErrInvalidSurfaceRange)
		}
	}
	if _, err := tx.Exec(`UPDATE context_surface_state SET generation = ? WHERE session_id = ?`, nextGeneration, store.sessionID); err != nil {
		return rollback(fmt.Errorf("advance context tool prune generation: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return core.ContextSurface{}, fmt.Errorf("commit context tool prune: %w", err)
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
WHERE id = ? AND session_id = ? AND status = ?`, core.CompactionFailed, message, timeValue(time.Now().UTC()), id, store.sessionID, core.CompactionStarted)
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
func (store *ContextSurfaceStore) Compactions() ([]core.CompactionLifecycle, error) {
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
	var result []core.CompactionLifecycle
	for rows.Next() {
		var lifecycle core.CompactionLifecycle
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
WHERE session_id = ? AND status = ?`, core.CompactionFailed, timeValue(time.Now().UTC()), store.sessionID, core.CompactionStarted)
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
	var generation int64
	if err := tx.QueryRow(`SELECT generation FROM context_surface_state WHERE session_id = ?`, store.sessionID).Scan(&generation); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("read context surface generation: %w", err)
	}
	var nodeCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM context_surface_nodes WHERE session_id = ?`, store.sessionID).Scan(&nodeCount); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("count context surface nodes: %w", err)
	}
	if nodeCount == 0 {
		rows, err := tx.Query(`SELECT seq FROM conversation_messages WHERE session_id = ? ORDER BY seq`, store.sessionID)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read conversation source sequences: %w", err)
		}
		position := 0
		for rows.Next() {
			var sequence int
			if err := rows.Scan(&sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("scan conversation source sequence: %w", err)
			}
			if _, err := tx.Exec(`
INSERT INTO context_surface_nodes(session_id, id, position, kind, source_start_seq, source_end_seq, content, generation)
VALUES (?, ?, ?, ?, ?, ?, '', ?)`, store.sessionID, newSurfaceID(), position, core.SurfaceNodeSource, sequence, sequence, generation); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("seed context surface source node: %w", err)
			}
			position++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("read conversation source sequences: %w", err)
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("close conversation source sequences: %w", err)
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
		rows, err := tx.Query(`SELECT seq FROM conversation_messages WHERE session_id = ? AND seq > ? ORDER BY seq`, store.sessionID, maxCovered.Int64)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read new conversation source sequences: %w", err)
		}
		for rows.Next() {
			var sequence int
			if err := rows.Scan(&sequence); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("scan new conversation source sequence: %w", err)
			}
			if _, err := tx.Exec(`
INSERT INTO context_surface_nodes(session_id, id, position, kind, source_start_seq, source_end_seq, content, generation)
VALUES (?, ?, ?, ?, ?, ?, '', ?)`, store.sessionID, newSurfaceID(), position, core.SurfaceNodeSource, sequence, sequence, generation); err != nil {
				_ = rows.Close()
				_ = tx.Rollback()
				return fmt.Errorf("append context surface source node: %w", err)
			}
			position++
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("read new conversation source sequences: %w", err)
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("close new conversation source sequences: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit context surface sync: %w", err)
	}
	return nil
}

func loadContextSurface(db *sql.DB, sessionID string) (core.ContextSurface, error) {
	generation, err := currentSurfaceGeneration(db, sessionID)
	if err != nil {
		return core.ContextSurface{}, err
	}
	rows, err := db.Query(`
SELECT id, position, kind, source_start_seq, source_end_seq, content, generation
FROM context_surface_nodes WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return core.ContextSurface{}, fmt.Errorf("read context surface nodes: %w", err)
	}
	defer rows.Close()
	surface := core.ContextSurface{SessionID: sessionID, Generation: generation}
	for rows.Next() {
		var node core.SurfaceNode
		if err := rows.Scan(&node.ID, &node.Position, &node.Kind, &node.SourceStartSeq, &node.SourceEndSeq, &node.Content, &node.Generation); err != nil {
			return core.ContextSurface{}, fmt.Errorf("scan context surface node: %w", err)
		}
		surface.Nodes = append(surface.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return core.ContextSurface{}, fmt.Errorf("read context surface nodes: %w", err)
	}
	return surface, nil
}

func loadContextSurfaceNodesTx(tx *sql.Tx, sessionID string) ([]core.SurfaceNode, error) {
	rows, err := tx.Query(`
SELECT id, position, kind, source_start_seq, source_end_seq, content, generation
FROM context_surface_nodes WHERE session_id = ? ORDER BY position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read context surface nodes: %w", err)
	}
	defer rows.Close()
	var nodes []core.SurfaceNode
	for rows.Next() {
		var node core.SurfaceNode
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

func currentSurfaceGenerationOrZero(db *sql.DB, sessionID string) (int64, error) {
	generation, err := currentSurfaceGeneration(db, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return generation, err
}

func currentSurfaceGenerationTx(tx *sql.Tx, sessionID string) (int64, error) {
	var generation int64
	if err := tx.QueryRow(`SELECT generation FROM context_surface_state WHERE session_id = ?`, sessionID).Scan(&generation); err != nil {
		return 0, fmt.Errorf("read context surface generation: %w", err)
	}
	return generation, nil
}

func selectedSurfaceRange(nodes []core.SurfaceNode, startSeq, endSeq int) (int, int) {
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

func validateBalancedToolPairsTx(tx *sql.Tx, sessionID string, startSeq, endSeq int) error {
	rows, err := tx.Query(`
SELECT message_seq, id FROM conversation_tool_calls
WHERE session_id = ? ORDER BY message_seq, position`, sessionID)
	if err != nil {
		return fmt.Errorf("read context surface tool calls: %w", err)
	}
	defer rows.Close()
	calls := make(map[string]int)
	for rows.Next() {
		var sequence int
		var id string
		if err := rows.Scan(&sequence, &id); err != nil {
			return fmt.Errorf("scan context surface tool call: %w", err)
		}
		if id != "" {
			calls[id] = sequence
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read context surface tool calls: %w", err)
	}
	resultRows, err := tx.Query(`
SELECT seq, tool_call_id FROM conversation_messages
WHERE session_id = ? AND role = ? AND tool_call_id <> '' ORDER BY seq`, sessionID, core.RoleTool)
	if err != nil {
		return fmt.Errorf("read context surface tool results: %w", err)
	}
	defer resultRows.Close()
	for resultRows.Next() {
		var sequence int
		var callID string
		if err := resultRows.Scan(&sequence, &callID); err != nil {
			return fmt.Errorf("scan context surface tool result: %w", err)
		}
		callSequence, found := calls[callID]
		if !found {
			return ErrInvalidSurfaceRange
		}
		callSelected := callSequence >= startSeq && callSequence <= endSeq
		resultSelected := sequence >= startSeq && sequence <= endSeq
		if callSelected != resultSelected {
			return ErrInvalidSurfaceRange
		}
	}
	if err := resultRows.Err(); err != nil {
		return fmt.Errorf("read context surface tool results: %w", err)
	}
	return nil
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
