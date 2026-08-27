package project

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ContextSummary is the durable compressed prefix of one session conversation.
type ContextSummary struct {
	SessionID         string
	CoveredThroughSeq int
	Content           string
	UpdatedAt         time.Time
}

// LoadContextSummary returns the one rolling summary for a session.
func (store *ProjectStore) LoadContextSummary(ctx context.Context, sessionID string) (ContextSummary, bool, error) {
	if store == nil || store.db == nil {
		return ContextSummary{}, false, fmt.Errorf("project store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ContextSummary{}, false, fmt.Errorf("summary session ID is empty")
	}
	var summary ContextSummary
	var updatedAt int64
	err := store.db.QueryRowContext(ctx, `SELECT covered_through_seq, content, updated_at FROM context_summaries WHERE session_id = ?`, sessionID).Scan(&summary.CoveredThroughSeq, &summary.Content, &updatedAt)
	if err == sql.ErrNoRows {
		return ContextSummary{}, false, nil
	}
	if err != nil {
		return ContextSummary{}, false, fmt.Errorf("load context summary: %w", err)
	}
	summary.SessionID = sessionID
	summary.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return summary, true, nil
}

// SaveContextSummary replaces the compressed prefix after it has covered more messages.
func (store *ProjectStore) SaveContextSummary(ctx context.Context, summary ContextSummary) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("project store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	summary.SessionID = strings.TrimSpace(summary.SessionID)
	summary.Content = strings.TrimSpace(summary.Content)
	if summary.SessionID == "" || summary.CoveredThroughSeq < 1 || summary.Content == "" {
		return fmt.Errorf("context summary is incomplete")
	}
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = time.Now().UTC()
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO context_summaries(session_id, covered_through_seq, content, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(session_id) DO UPDATE SET covered_through_seq = excluded.covered_through_seq, content = excluded.content, updated_at = excluded.updated_at`, summary.SessionID, summary.CoveredThroughSeq, summary.Content, summary.UpdatedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("save context summary: %w", err)
	}
	return nil
}
