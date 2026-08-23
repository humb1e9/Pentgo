package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pentgo/internal/domain"
)

// ErrFactNotFound identifies a missing project fact.
var ErrFactNotFound = errors.New("project fact not found")

// ProjectFactRepository owns project-scoped minimal facts through the shared
// ProjectStore database connection. ProjectStore is the sole connection owner.
type ProjectFactRepository struct {
	db        *sql.DB
	projectID string
}

// OpenProjectFactRepository opens the minimal fact repository for the singleton
// project. It does not own the connection; ProjectStore.Close remains the sole
// owner.
func (store *ProjectStore) OpenProjectFactRepository() (*ProjectFactRepository, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project store is nil")
	}
	var projectID string
	if err := store.db.QueryRow(`SELECT id FROM projects WHERE singleton = 1`).Scan(&projectID); err != nil {
		return nil, fmt.Errorf("load project id for fact repository: %w", err)
	}
	return &ProjectFactRepository{db: store.db, projectID: projectID}, nil
}

// Upsert inserts or replaces a single fact row for the repository's project.
func (repo *ProjectFactRepository) Upsert(ctx context.Context, fact domain.ProjectFact) error {
	if repo == nil || repo.db == nil {
		return fmt.Errorf("project fact repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := repo.db.ExecContext(ctx, `
INSERT INTO project_facts(project_id, fact_key, value, evidence_ref, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(project_id, fact_key) DO UPDATE SET
    value = excluded.value,
    evidence_ref = excluded.evidence_ref,
    updated_at = excluded.updated_at`,
		repo.projectID, fact.Key, fact.Value,
		nullableInt(fact.EvidenceRef), timeValue(now),
	)
	if err != nil {
		return fmt.Errorf("upsert project fact: %w", err)
	}
	return nil
}

// Get returns the fact for the given key. Returns false when the key does not
// exist.
func (repo *ProjectFactRepository) Get(ctx context.Context, key string) (domain.ProjectFact, bool, error) {
	if repo == nil || repo.db == nil {
		return domain.ProjectFact{}, false, fmt.Errorf("project fact repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return domain.ProjectFact{}, false, err
	}
	var fact domain.ProjectFact
	var ref sql.NullInt64
	var updatedAt int64
	err := repo.db.QueryRowContext(ctx,
		`SELECT fact_key, value, evidence_ref, updated_at FROM project_facts WHERE project_id = ? AND fact_key = ?`,
		repo.projectID, key,
	).Scan(&fact.Key, &fact.Value, &ref, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectFact{}, false, nil
	}
	if err != nil {
		return domain.ProjectFact{}, false, fmt.Errorf("get project fact: %w", err)
	}
	if ref.Valid {
		value := int(ref.Int64)
		fact.EvidenceRef = &value
	}
	fact.UpdatedAt = parseTime(updatedAt)
	return fact, true, nil
}

// List returns all facts for the repository's project, sorted by key
// ascending. The returned slice is always non-nil.
func (repo *ProjectFactRepository) List(ctx context.Context) ([]domain.ProjectFact, error) {
	if repo == nil || repo.db == nil {
		return nil, fmt.Errorf("project fact repository is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repo.db.QueryContext(ctx,
		`SELECT fact_key, value, evidence_ref, updated_at FROM project_facts WHERE project_id = ? ORDER BY fact_key ASC`,
		repo.projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project facts: %w", err)
	}
	defer rows.Close()
	var facts []domain.ProjectFact
	for rows.Next() {
		var fact domain.ProjectFact
		var ref sql.NullInt64
		var updatedAt int64
		if err := rows.Scan(&fact.Key, &fact.Value, &ref, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan project fact: %w", err)
		}
		if ref.Valid {
			value := int(ref.Int64)
			fact.EvidenceRef = &value
		}
		fact.UpdatedAt = parseTime(updatedAt)
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list project facts rows: %w", err)
	}
	if facts == nil {
		facts = []domain.ProjectFact{}
	}
	return facts, nil
}

func nullableInt(ref *int) any {
	if ref == nil {
		return nil
	}
	return *ref
}
