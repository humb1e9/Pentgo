package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pentgo/internal/domain"
)

var (
	// ErrFactNotFound identifies a missing project fact for Get/Deprecate/Restore.
	ErrFactNotFound = errors.New("project fact not found")
	// ErrFactEvidenceMissing identifies confirmed facts without successful evidence.
	ErrFactEvidenceMissing = errors.New("confirmed project fact requires successful evidence")
	// ErrFactEdgeTargetMissing identifies an edge whose target fact does not exist.
	ErrFactEdgeTargetMissing = errors.New("project fact edge target does not exist")
)

// ProjectFactWrite carries one upserted fact and its replacement edge set.
type ProjectFactWrite struct {
	Fact  domain.ProjectFact
	Edges []domain.ProjectFactEdge
}

// FactQuery filters the default deterministic project-fact listing.
type FactQuery struct {
	Category   string
	Confidence string
	Limit      int
}

// FactIndexResult is the bounded model-visible Fact Index plus omission
// metadata used for context activity reporting.
type FactIndexResult struct {
	Text      string
	Shown     int
	Omitted   int
	Truncated bool
}

// ProjectFactStore owns project-scoped structured facts. It shares the project
// database connection and serializes write transactions so Evidence, facts,
// and edges stay internally consistent.
type ProjectFactStore struct {
	mu        sync.Mutex
	db        *sql.DB
	projectID string
	closed    bool
}

// OpenProjectFacts opens the structured fact ledger for the singleton project.
func (store *ProjectStore) OpenProjectFacts() (*ProjectFactStore, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project store is nil")
	}
	var projectID string
	if err := store.db.QueryRow(`SELECT id FROM projects WHERE singleton = 1`).Scan(&projectID); err != nil {
		return nil, fmt.Errorf("load project id for fact store: %w", err)
	}
	return &ProjectFactStore{db: store.db, projectID: projectID}, nil
}

// Upsert atomically writes a fact, its evidence joins, and the supplied edge
// set. Confirmed facts require every Evidence ref to exist and be successful.
func (store *ProjectFactStore) Upsert(ctx context.Context, write ProjectFactWrite) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("project fact store is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fact := domain.CloneProjectFact(write.Fact)
	fact.FactKey = strings.TrimSpace(fact.FactKey)
	fact.Category = strings.TrimSpace(fact.Category)
	fact.Summary = strings.TrimSpace(fact.Summary)
	fact.Body = strings.TrimSpace(fact.Body)
	fact.Confidence = strings.TrimSpace(fact.Confidence)
	fact.EvidenceRefs = domain.NormalizeEvidenceRefs(fact.EvidenceRefs)
	if err := domain.ValidateProjectFact(fact); err != nil {
		return err
	}
	if fact.ProjectID == "" {
		fact.ProjectID = store.projectID
	}
	if fact.ProjectID != store.projectID {
		return fmt.Errorf("project fact belongs to another project")
	}
	if fact.ID == "" {
		fact.ID = newID("fact")
	}
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = time.Now().UTC()
	}
	if fact.UpdatedAt.IsZero() {
		fact.UpdatedAt = fact.CreatedAt
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return fmt.Errorf("project fact store is closed")
	}
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin project fact upsert: %w", err)
	}
	if fact.Confidence == domain.FactConfidenceConfirmed {
		if err := verifyEvidenceRefsTx(ctx, tx, fact.EvidenceRefs); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`
INSERT INTO project_facts(
    id, project_id, fact_key, category, summary, body, confidence, pinned,
    source_session_id, created_at, updated_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, fact_key) DO UPDATE SET
    category = excluded.category,
    summary = excluded.summary,
    body = excluded.body,
    confidence = excluded.confidence,
    pinned = excluded.pinned,
    source_session_id = excluded.source_session_id,
    updated_at = excluded.updated_at`,
		fact.ID, fact.ProjectID, fact.FactKey, fact.Category, fact.Summary, fact.Body,
		fact.Confidence, boolInt(fact.Pinned), nullableText(fact.SourceSessionID),
		timeValue(fact.CreatedAt), timeValue(fact.UpdatedAt),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("upsert project fact: %w", err)
	}
	var factID string
	if err := tx.QueryRow(`SELECT id FROM project_facts WHERE project_id = ? AND fact_key = ?`, fact.ProjectID, fact.FactKey).Scan(&factID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("load upserted project fact id: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM project_fact_evidence WHERE fact_id = ?`, factID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("replace project fact evidence: %w", err)
	}
	for _, reference := range fact.EvidenceRefs {
		if _, err := tx.Exec(`INSERT INTO project_fact_evidence(fact_id, evidence_seq) VALUES(?, ?)`, factID, reference); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert project fact evidence: %w", err)
		}
	}
	if err := replaceFactEdgesTx(ctx, tx, store.projectID, fact.FactKey, write.Edges); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project fact upsert: %w", err)
	}
	return nil
}

// Get returns the complete fact (including Body and Evidence refs).
func (store *ProjectFactStore) Get(key string) (domain.ProjectFact, bool, error) {
	if store == nil || store.db == nil {
		return domain.ProjectFact{}, false, fmt.Errorf("project fact store is nil")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return domain.ProjectFact{}, false, fmt.Errorf("project fact key is required")
	}
	fact, err := store.queryFact(context.Background(), `pf.project_id = ? AND pf.fact_key = ?`, store.projectID, key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectFact{}, false, nil
	}
	if err != nil {
		return domain.ProjectFact{}, false, err
	}
	return fact, true, nil
}

// List returns non-deprecated or filtered facts in deterministic index order.
func (store *ProjectFactStore) List(query FactQuery) ([]domain.ProjectFact, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project fact store is nil")
	}
	conditions := []string{"pf.project_id = ?"}
	args := []any{store.projectID}
	if strings.TrimSpace(query.Category) != "" {
		conditions = append(conditions, "pf.category = ?")
		args = append(args, strings.TrimSpace(query.Category))
	}
	if strings.TrimSpace(query.Confidence) != "" {
		conditions = append(conditions, "pf.confidence = ?")
		args = append(args, strings.TrimSpace(query.Confidence))
	}
	return store.queryFacts(context.Background(), conditions, args, factQueryLimit(query.Limit))
}

// Search returns facts whose key, summary, or body contains the bounded query.
func (store *ProjectFactStore) Search(query string, filter FactQuery) ([]domain.ProjectFact, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project fact store is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("project fact search query is required")
	}
	conditions := []string{"pf.project_id = ?", "(pf.fact_key LIKE ? OR pf.summary LIKE ? OR pf.body LIKE ?)"}
	pattern := "%" + query + "%"
	args := []any{store.projectID, pattern, pattern, pattern}
	if strings.TrimSpace(filter.Category) != "" {
		conditions = append(conditions, "pf.category = ?")
		args = append(args, strings.TrimSpace(filter.Category))
	}
	if strings.TrimSpace(filter.Confidence) != "" {
		conditions = append(conditions, "pf.confidence = ?")
		args = append(args, strings.TrimSpace(filter.Confidence))
	}
	return store.queryFacts(context.Background(), conditions, args, factQueryLimit(filter.Limit))
}

// Deprecate marks a fact as deprecated while retaining its audit row.
func (store *ProjectFactStore) Deprecate(key string) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("project fact store is nil")
	}
	return store.updateConfidence(context.Background(), key, domain.FactConfidenceDeprecated, false)
}

// Restore returns a fact to the supplied non-deprecated confidence. Restoring a
// confirmed fact requires the retained evidence refs to be successful.
func (store *ProjectFactStore) Restore(key, confidence string) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("project fact store is nil")
	}
	confidence = strings.TrimSpace(confidence)
	if confidence != domain.FactConfidenceConfirmed && confidence != domain.FactConfidenceTentative {
		return fmt.Errorf("restored project fact confidence must be confirmed or tentative")
	}
	return store.updateConfidence(context.Background(), key, confidence, confidence == domain.FactConfidenceConfirmed)
}

// FactIndex renders a bounded deterministic index for model context.
func (store *ProjectFactStore) FactIndex(tokenBudget int) (FactIndexResult, error) {
	if store == nil || store.db == nil {
		return FactIndexResult{}, fmt.Errorf("project fact store is nil")
	}
	if tokenBudget < 0 {
		tokenBudget = 0
	}
	facts, err := store.queryFacts(context.Background(), []string{"pf.project_id = ?", "pf.confidence <> ?"}, []any{store.projectID, domain.FactConfidenceDeprecated}, 0)
	if err != nil {
		return FactIndexResult{}, err
	}
	edges, err := store.queryEdges(context.Background(), []string{"pe.project_id = ?", "pe.confidence <> ?"}, []any{store.projectID, domain.FactConfidenceDeprecated})
	if err != nil {
		return FactIndexResult{}, err
	}
	facts = sortFactIndex(facts)
	indexByKey := make(map[string]domain.ProjectFact, len(facts))
	for _, fact := range facts {
		indexByKey[fact.FactKey] = fact
	}
	lines := make([]string, 0, len(facts))
	used := 0
	omitted := 0
	for _, fact := range facts {
		line := formatFactIndexLine(fact, edges, indexByKey)
		cost := estimateFactTokens(line)
		if len(lines) != 0 && used+cost > tokenBudget {
			omitted += len(facts) - len(lines)
			break
		}
		lines = append(lines, line)
		used += cost
	}
	if len(lines) == 0 && len(facts) != 0 {
		omitted = len(facts)
	}
	truncated := omitted > 0
	text := fmt.Sprintf(`<project-fact-index shown="%d" omitted="%d" truncated="%t">`, len(lines), omitted, truncated)
	if len(lines) != 0 {
		text += "\n" + strings.Join(lines, "\n") + "\n"
	}
	text += "</project-fact-index>"
	return FactIndexResult{Text: text, Shown: len(lines), Omitted: omitted, Truncated: truncated}, nil
}

// Close marks the store read-only. The shared database remains owned by
// ProjectStore, so Close only prevents further fact writes.
func (store *ProjectFactStore) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.closed = true
	return nil
}

func (store *ProjectFactStore) queryFact(ctx context.Context, where string, args ...any) (domain.ProjectFact, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProjectFact{}, err
	}
	facts, err := store.queryFacts(ctx, []string{where}, args, 1)
	if err != nil {
		return domain.ProjectFact{}, err
	}
	if len(facts) == 0 {
		return domain.ProjectFact{}, sql.ErrNoRows
	}
	return facts[0], nil
}

func (store *ProjectFactStore) queryFacts(ctx context.Context, conditions []string, args []any, limit int) ([]domain.ProjectFact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	where := " WHERE " + strings.Join(conditions, " AND ")
	rows, err := store.db.QueryContext(ctx, `
SELECT pf.id, pf.project_id, pf.fact_key, pf.category, pf.summary, pf.body,
       pf.confidence, pf.pinned, pf.source_session_id, pf.created_at, pf.updated_at
FROM project_facts pf`+where+`
ORDER BY pf.pinned DESC, `+factCategoryOrderSQL()+`, pf.updated_at DESC, pf.fact_key ASC`+factLimitSQL(limit), args...)
	if err != nil {
		return nil, fmt.Errorf("query project facts: %w", err)
	}
	defer rows.Close()
	var facts []domain.ProjectFact
	for rows.Next() {
		var fact domain.ProjectFact
		var pinned int
		var sessionID sql.NullString
		var createdAt, updatedAt int64
		if err := rows.Scan(&fact.ID, &fact.ProjectID, &fact.FactKey, &fact.Category, &fact.Summary, &fact.Body, &fact.Confidence, &pinned, &sessionID, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan project fact: %w", err)
		}
		fact.Pinned = pinned != 0
		fact.SourceSessionID = sessionID.String
		fact.CreatedAt = parseTime(createdAt)
		fact.UpdatedAt = parseTime(updatedAt)
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query project facts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close project fact rows: %w", err)
	}
	for index := range facts {
		references, err := store.loadEvidenceRefs(ctx, facts[index].ID)
		if err != nil {
			return nil, err
		}
		facts[index].EvidenceRefs = references
	}
	return facts, nil
}

func (store *ProjectFactStore) loadEvidenceRefs(ctx context.Context, factID string) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT evidence_seq FROM project_fact_evidence WHERE fact_id = ? ORDER BY evidence_seq`, factID)
	if err != nil {
		return nil, fmt.Errorf("query project fact evidence: %w", err)
	}
	defer rows.Close()
	var refs []int
	for rows.Next() {
		var reference int
		if err := rows.Scan(&reference); err != nil {
			return nil, fmt.Errorf("scan project fact evidence: %w", err)
		}
		refs = append(refs, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query project fact evidence: %w", err)
	}
	return refs, nil
}

func (store *ProjectFactStore) queryEdges(ctx context.Context, conditions []string, args []any) ([]domain.ProjectFactEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	where := " WHERE " + strings.Join(conditions, " AND ")
	rows, err := store.db.QueryContext(ctx, `
SELECT pe.id, pe.project_id, pe.source_fact_key, pe.target_fact_key,
       pe.edge_type, pe.confidence, pe.created_at, pe.updated_at
FROM project_fact_edges pe`+where+`
ORDER BY pe.source_fact_key ASC, pe.edge_type ASC, pe.target_fact_key ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("query project fact edges: %w", err)
	}
	defer rows.Close()
	var edges []domain.ProjectFactEdge
	for rows.Next() {
		var edge domain.ProjectFactEdge
		var createdAt, updatedAt int64
		if err := rows.Scan(&edge.ID, &edge.ProjectID, &edge.SourceFactKey, &edge.TargetFactKey, &edge.EdgeType, &edge.Confidence, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan project fact edge: %w", err)
		}
		edge.CreatedAt = parseTime(createdAt)
		edge.UpdatedAt = parseTime(updatedAt)
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query project fact edges: %w", err)
	}
	return edges, nil
}

func (store *ProjectFactStore) updateConfidence(ctx context.Context, key, confidence string, requireEvidence bool) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("project fact key is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return fmt.Errorf("project fact store is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin project fact confidence update: %w", err)
	}
	var factID string
	if err := tx.QueryRow(`SELECT id FROM project_facts WHERE project_id = ? AND fact_key = ?`, store.projectID, key).Scan(&factID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFactNotFound
		}
		return fmt.Errorf("load project fact confidence: %w", err)
	}
	if requireEvidence {
		refs, err := loadEvidenceRefsTx(ctx, tx, factID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := verifyEvidenceRefsTx(ctx, tx, refs); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE project_facts SET confidence = ?, updated_at = ? WHERE id = ?`, confidence, timeValue(time.Now().UTC()), factID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update project fact confidence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project fact confidence update: %w", err)
	}
	return nil
}

func verifyEvidenceRefsTx(ctx context.Context, tx *sql.Tx, references []int) error {
	if len(references) == 0 {
		return nil
	}
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return err
		}
		var success int
		err := tx.QueryRow(`SELECT success FROM evidence_records WHERE seq = ?`, reference).Scan(&success)
		if errors.Is(err, sql.ErrNoRows) || err == nil && success == 0 {
			return fmt.Errorf("%w: seq %d", ErrFactEvidenceMissing, reference)
		}
		if err != nil {
			return fmt.Errorf("query project fact evidence record %d: %w", reference, err)
		}
	}
	return nil
}

func loadEvidenceRefsTx(ctx context.Context, tx *sql.Tx, factID string) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT evidence_seq FROM project_fact_evidence WHERE fact_id = ? ORDER BY evidence_seq`, factID)
	if err != nil {
		return nil, fmt.Errorf("load project fact evidence refs: %w", err)
	}
	defer rows.Close()
	var refs []int
	for rows.Next() {
		var reference int
		if err := rows.Scan(&reference); err != nil {
			return nil, fmt.Errorf("scan project fact evidence ref: %w", err)
		}
		refs = append(refs, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load project fact evidence refs: %w", err)
	}
	return refs, nil
}

func replaceFactEdgesTx(ctx context.Context, tx *sql.Tx, projectID, sourceKey string, edges []domain.ProjectFactEdge) error {
	if _, err := tx.Exec(`DELETE FROM project_fact_edges WHERE project_id = ? AND source_fact_key = ?`, projectID, sourceKey); err != nil {
		return fmt.Errorf("replace project fact edges: %w", err)
	}
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		edge.SourceFactKey = strings.TrimSpace(edge.SourceFactKey)
		edge.TargetFactKey = strings.TrimSpace(edge.TargetFactKey)
		edge.EdgeType = strings.TrimSpace(edge.EdgeType)
		edge.Confidence = strings.TrimSpace(edge.Confidence)
		if edge.SourceFactKey != sourceKey {
			return fmt.Errorf("project fact edge source must equal upserted fact key")
		}
		if err := domain.ValidateProjectFactEdge(edge); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_facts WHERE project_id = ? AND fact_key = ?)`, projectID, edge.TargetFactKey).Scan(&exists); err != nil {
			return fmt.Errorf("check project fact edge target: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("%w: %s", ErrFactEdgeTargetMissing, edge.TargetFactKey)
		}
		if edge.ID == "" {
			edge.ID = newID("fact-edge")
		}
		if edge.ProjectID == "" {
			edge.ProjectID = projectID
		}
		if edge.CreatedAt.IsZero() {
			edge.CreatedAt = time.Now().UTC()
		}
		if edge.UpdatedAt.IsZero() {
			edge.UpdatedAt = edge.CreatedAt
		}
		if _, err := tx.Exec(`
INSERT INTO project_fact_edges(
    id, project_id, source_fact_key, target_fact_key, edge_type, confidence,
    created_at, updated_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, source_fact_key, target_fact_key, edge_type) DO UPDATE SET
    confidence = excluded.confidence,
    updated_at = excluded.updated_at`,
			edge.ID, projectID, edge.SourceFactKey, edge.TargetFactKey, edge.EdgeType,
			edge.Confidence, timeValue(edge.CreatedAt), timeValue(edge.UpdatedAt),
		); err != nil {
			return fmt.Errorf("upsert project fact edge: %w", err)
		}
	}
	return nil
}

func sortFactIndex(facts []domain.ProjectFact) []domain.ProjectFact {
	result := append([]domain.ProjectFact(nil), facts...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.Pinned != right.Pinned {
			return left.Pinned
		}
		leftPriority, rightPriority := factCategoryPriority(left.Category), factCategoryPriority(right.Category)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		return left.FactKey < right.FactKey
	})
	return result
}

func factCategoryPriority(category string) int {
	switch category {
	case domain.FactCategoryTarget:
		return 0
	case domain.FactCategoryFinding, domain.FactCategoryChain:
		return 1
	case domain.FactCategoryExploit, domain.FactCategoryPOC:
		return 2
	case domain.FactCategoryAuth, domain.FactCategoryInfra, domain.FactCategoryBusiness:
		return 3
	default:
		return 4
	}
}

func factCategoryOrderSQL() string {
	priorities := []struct {
		priority int
		value    string
	}{
		{0, "'target'"},
		{1, "'finding'"},
		{1, "'chain'"},
		{2, "'exploit'"},
		{2, "'poc'"},
		{3, "'auth'"},
		{3, "'infra'"},
		{3, "'business'"},
	}
	parts := make([]string, 0, len(priorities)+1)
	for _, item := range priorities {
		parts = append(parts, fmt.Sprintf("WHEN category = %s THEN %d", item.value, item.priority))
	}
	parts = append(parts, "ELSE 4")
	return "CASE " + strings.Join(parts, " ") + " END"
}

func factLimitSQL(limit int) string {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	return fmt.Sprintf(" LIMIT %d", limit)
}

func factQueryLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 100
	}
	return limit
}

func formatFactIndexLine(fact domain.ProjectFact, edges []domain.ProjectFactEdge, facts map[string]domain.ProjectFact) string {
	var builder strings.Builder
	builder.WriteString("- ")
	builder.WriteString(fact.FactKey)
	builder.WriteString(": ")
	builder.WriteString(fact.Summary)
	builder.WriteString(" [")
	builder.WriteString(fact.Confidence)
	builder.WriteString("] [")
	builder.WriteString(fact.Category)
	builder.WriteString("]")
	if fact.Pinned {
		builder.WriteString(" [pinned]")
	}
	hints := make([]string, 0, len(edges))
	for _, edge := range edges {
		source, sourceVisible := facts[edge.SourceFactKey]
		target, targetVisible := facts[edge.TargetFactKey]
		if !sourceVisible || !targetVisible || source.Confidence == domain.FactConfidenceDeprecated || target.Confidence == domain.FactConfidenceDeprecated {
			continue
		}
		if edge.SourceFactKey == fact.FactKey || edge.TargetFactKey == fact.FactKey {
			hints = append(hints, fmt.Sprintf("%s -[%s]-> %s", edge.SourceFactKey, edge.EdgeType, edge.TargetFactKey))
		}
	}
	sort.Strings(hints)
	for _, hint := range hints {
		builder.WriteString("\n  ")
		builder.WriteString(hint)
	}
	return builder.String()
}

func estimateFactTokens(value string) int {
	if value == "" {
		return 0
	}
	return (utf8.RuneCountInString(value) + 3) / 4
}
