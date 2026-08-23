package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/domain"
)

func TestV5FactsMigrateToTentativeNotes(t *testing.T) {
	store := openV5FactFixture(t, []legacyFact{{Key: "host", Value: "10.0.0.8"}})
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	got, found, err := facts.Get("host")
	if err != nil {
		t.Fatal(err)
	}
	if !found || got.Category != domain.FactCategoryNote || got.Confidence != domain.FactConfidenceTentative || got.Summary != "10.0.0.8" || got.Body != "10.0.0.8" {
		t.Fatalf("migrated fact = %#v found=%v", got, found)
	}
}

func TestV5FactsMigratePreservesSessionAndTimestamp(t *testing.T) {
	base := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store := openV5FactFixture(t, []legacyFact{
		{
			Key: "host", Value: "10.0.0.8",
			SessionID: "session-v5-test", At: base, UpdatedAt: base.Add(time.Hour),
		},
	})
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	got, found, err := facts.Get("host")
	if err != nil || !found {
		t.Fatalf("get = %v found=%v", err, found)
	}
	if got.SourceSessionID != "session-v5-test" || !got.CreatedAt.Equal(base) || !got.UpdatedAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("migrated metadata = %#v", got)
	}
}

func TestV5FactsMigrateMultipleKeys(t *testing.T) {
	store := openV5FactFixture(t, []legacyFact{
		{Key: "host", Value: "10.0.0.8"},
		{Key: "port", Value: "443"},
	})
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	all, err := facts.List(FactQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("migrated fact count = %d, want 2", len(all))
	}
}

func TestUpsertConfirmedFactRequiresSuccessfulEvidence(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	err = facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "target", Category: domain.FactCategoryTarget, Summary: "target",
			Body: "target body", Confidence: domain.FactConfidenceConfirmed,
			EvidenceRefs: []int{1}, CreatedAt: now, UpdatedAt: now,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires successful evidence") {
		t.Fatalf("expected evidence error, got %v", err)
	}
	// Verify fact was not persisted
	_, found, err := facts.Get("target")
	if err != nil || found {
		t.Fatalf("fact persisted despite evidence failure: found=%v err=%v", found, err)
	}
}

func TestUpsertConfirmedFactWithSuccessfulEvidenceSucceeds(t *testing.T) {
	store := insertEvidenceFixture(t, 1, true)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	err = facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "target", Category: domain.FactCategoryTarget, Summary: "API host",
			Body: "10.0.0.8:443", Confidence: domain.FactConfidenceConfirmed,
			EvidenceRefs: []int{1}, CreatedAt: now, UpdatedAt: now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := facts.Get("target")
	if err != nil || !found || got.Confidence != domain.FactConfidenceConfirmed || len(got.EvidenceRefs) != 1 || got.EvidenceRefs[0] != 1 {
		t.Fatalf("fact = %#v found=%v err=%v", got, found, err)
	}
}

func TestUpsertAtomicRollbackOnEdgeTargetMissing(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	// Upsert edge to non-existent target
	err = facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "source", Category: domain.FactCategoryNote, Summary: "src",
			Body: "source body", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
		Edges: []domain.ProjectFactEdge{{
			SourceFactKey: "source", TargetFactKey: "missing",
			EdgeType: domain.FactEdgeDependsOn, Confidence: domain.FactConfidenceTentative,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "edge target does not exist") {
		t.Fatalf("expected edge target error, got %v", err)
	}
	// Verify fact was not persisted
	_, found, err := facts.Get("source")
	if err != nil || found {
		t.Fatalf("fact persisted despite edge failure: found=%v err=%v", found, err)
	}
}

func TestUpsertEdgesAtomically(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	// Upsert source fact first
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "source", Category: domain.FactCategoryNote, Summary: "src",
			Body: "source body", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Upsert target fact
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "target", Category: domain.FactCategoryTarget, Summary: "tgt",
			Body: "target body", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Upsert source with edges
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "source", Category: domain.FactCategoryNote, Summary: "src",
			Body: "source body", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
		Edges: []domain.ProjectFactEdge{{
			SourceFactKey: "source", TargetFactKey: "target",
			EdgeType: domain.FactEdgeDependsOn, Confidence: domain.FactConfidenceTentative,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// Deprecate target and verify edge still shows in index
	if err := facts.Deprecate("target"); err != nil {
		t.Fatal(err)
	}
	index, err := facts.FactIndex(1000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(index.Text, "depends_on") {
		t.Fatalf("deprecated edge hint in index: %q", index.Text)
	}
}

func TestDeprecateMarkersFactAndRetainsAuditRows(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "host", Category: domain.FactCategoryNote, Summary: "10.0.0.8",
			Body: "10.0.0.8", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := facts.Deprecate("host"); err != nil {
		t.Fatal(err)
	}
	got, found, err := facts.Get("host")
	if err != nil || !found || got.Confidence != domain.FactConfidenceDeprecated {
		t.Fatalf("deprecated fact = %#v found=%v err=%v", got, found, err)
	}
	index, err := facts.FactIndex(1000)
	if err != nil {
		t.Fatal(err)
	}
	if index.Shown != 0 {
		t.Fatalf("deprecated fact in index: %q", index.Text)
	}
}

func TestRestoreRejectsConfirmedWithoutExistingEvidence(t *testing.T) {
	store := insertEvidenceFixture(t, 1, false)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "host", Category: domain.FactCategoryNote, Summary: "10.0.0.8",
			Body: "10.0.0.8", Confidence: domain.FactConfidenceTentative,
			EvidenceRefs: []int{1}, CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := facts.Deprecate("host"); err != nil {
		t.Fatal(err)
	}
	if err := facts.Restore("host", domain.FactConfidenceConfirmed); err == nil {
		t.Fatal("restore confirmed without successful evidence accepted")
	}
}

func TestRestoreTentativeWorksWithoutEvidence(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	if err := facts.Upsert(context.Background(), ProjectFactWrite{
		Fact: domain.ProjectFact{
			FactKey: "host", Category: domain.FactCategoryNote, Summary: "10.0.0.8",
			Body: "10.0.0.8", Confidence: domain.FactConfidenceTentative,
			CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := facts.Deprecate("host"); err != nil {
		t.Fatal(err)
	}
	if err := facts.Restore("host", domain.FactConfidenceTentative); err != nil {
		t.Fatal(err)
	}
	got, found, err := facts.Get("host")
	if err != nil || !found || got.Confidence != domain.FactConfidenceTentative {
		t.Fatalf("restored fact = %#v found=%v err=%v", got, found, err)
	}
}

func TestFactIndexOrdersByPinnedCategoryUpdatedAtKey(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	first := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	inputs := []domain.ProjectFact{
		{FactKey: "note-key", Category: domain.FactCategoryNote, Summary: "note", Body: "note body", Confidence: domain.FactConfidenceTentative, CreatedAt: now, UpdatedAt: first},
		{FactKey: "target-key", Category: domain.FactCategoryTarget, Summary: "target", Body: "target body", Confidence: domain.FactConfidenceConfirmed, EvidenceRefs: []int{1}, CreatedAt: now, UpdatedAt: first.Add(time.Minute), Pinned: true},
		{FactKey: "finding-key", Category: domain.FactCategoryFinding, Summary: "finding", Body: "finding body", Confidence: domain.FactConfidenceTentative, CreatedAt: now, UpdatedAt: first.Add(2 * time.Minute)},
		{FactKey: "auth-key", Category: domain.FactCategoryAuth, Summary: "auth", Body: "auth body", Confidence: domain.FactConfidenceTentative, CreatedAt: now, UpdatedAt: first.Add(3 * time.Minute)},
	}
	// Insert evidence row for the confirmed fact in this project's database.
	if _, err := store.db.Exec(`INSERT INTO evidence_records(tool, arguments_json, success, output, started_at, finished_at) VALUES('test', '{}', 1, '', ?, ?)`, timeValue(now), timeValue(now)); err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if err := facts.Upsert(context.Background(), ProjectFactWrite{Fact: input}); err != nil {
			t.Fatal(err)
		}
	}
	index, err := facts.FactIndex(2000)
	if err != nil {
		t.Fatal(err)
	}
	if index.Shown != 4 || index.Truncated {
		t.Fatalf("index = %#v", index)
	}
	// Pinned target should be first, then finding, then auth, then note
	order := []string{"target-key", "finding-key", "auth-key", "note-key"}
	previous := -1
	for _, key := range order {
		position := strings.Index(index.Text, key)
		if position < previous {
			t.Fatalf("key %q out of order in index: %q", key, index.Text)
		}
		previous = position
	}
}

func TestFactIndexTruncatesWhenOverBudget(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	now := time.Now().UTC()
	for index := 0; index < 20; index++ {
		key := fmt.Sprintf("fact-%d", index)
		if err := facts.Upsert(context.Background(), ProjectFactWrite{
			Fact: domain.ProjectFact{
				FactKey: key, Category: domain.FactCategoryNote, Summary: strings.Repeat(key, 5),
				Body: "body", Confidence: domain.FactConfidenceTentative,
				CreatedAt: now, UpdatedAt: now,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	index, err := facts.FactIndex(10)
	if err != nil {
		t.Fatal(err)
	}
	if !index.Truncated || index.Omitted == 0 || index.Shown+index.Omitted != 20 {
		t.Fatalf("index truncated = %#v", index)
	}
}

// legacyFact mirrors the v5 facts table row for test fixtures.
type legacyFact struct {
	Key       string
	Value     string
	SessionID string
	At        time.Time
	UpdatedAt time.Time
}

// openV5FactFixture creates a v5 database with the given legacy facts, then
// opens it through OpenProjectStore so the v5→v6 migration runs.
func openV5FactFixture(t *testing.T, legacy []legacyFact) *ProjectStore {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pentgo"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s/.pentgo/pentgo.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    intent TEXT NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    last_turn_id TEXT,
    active_turn_id TEXT,
    final_summary TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS evidence_records (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    tool TEXT NOT NULL,
    arguments_json TEXT NOT NULL,
    success INTEGER NOT NULL CHECK(success IN (0,1)),
    output TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS facts (
    key TEXT PRIMARY KEY,
    position INTEGER NOT NULL UNIQUE CHECK (position >= 0),
    value TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
PRAGMA user_version = 5;`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO projects(singleton, id, name, created_at, updated_at) VALUES(1, ?, ?, ?, ?)`, "project-v5", "v5 test", timeValue(now), timeValue(now)); err != nil {
		t.Fatal(err)
	}
	seenSessions := make(map[string]bool)
	for _, fact := range legacy {
		if fact.SessionID == "" || seenSessions[fact.SessionID] {
			continue
		}
		if _, err := db.Exec(`INSERT INTO sessions(id, name, started_at, updated_at) VALUES(?, 'v5', ?, ?)`, fact.SessionID, timeValue(now), timeValue(now)); err != nil {
			t.Fatal(err)
		}
		seenSessions[fact.SessionID] = true
	}
	for position, fact := range legacy {
		if fact.At.IsZero() {
			fact.At = now
		}
		if fact.UpdatedAt.IsZero() {
			fact.UpdatedAt = fact.At
		}
		if _, err := db.Exec(`INSERT INTO facts(key, position, value, source, session_id, at, updated_at) VALUES(?, ?, ?, '', ?, ?, ?)`,
			fact.Key, position, fact.Value, nullableText(fact.SessionID), timeValue(fact.At), timeValue(fact.UpdatedAt)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	store, err := OpenProjectStore(root + "/.pentgo")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// insertEvidenceFixture creates a store with one evidence row at the given seq
// (AUTOINCREMENT) with the given success value.
func insertEvidenceFixture(t *testing.T, expectedSeq int, success bool) *ProjectStore {
	t.Helper()
	store := newTestStore(t)
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO evidence_records(tool, arguments_json, success, output, started_at, finished_at) VALUES(?, ?, ?, ?, ?, ?)`,
		"test", "{}", boolInt(success), "output", timeValue(now), timeValue(now)); err != nil {
		t.Fatal(err)
	}
	return store
}
