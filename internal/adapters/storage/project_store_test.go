package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

func TestProjectStorePersistsNormalizedSessionGraph(t *testing.T) {
	store := newTestStore(t)
	startedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	session := domain.NewSession("session-normalized", "inspect", startedAt)
	session.Name = "Primary"
	session.AddTargets("https://TARGET", "https://TARGET/api")
	turn, err := session.BeginTurn("turn-1", "inspect TARGET", startedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.FinishTurn(turn.ID, "complete", startedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenProjectStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.LoadSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != session.Name || len(loaded.Targets) != 2 || loaded.ActiveTurn == nil || loaded.ActiveTurn.ID != turn.ID || loaded.FinalSummary != "complete" {
		t.Fatalf("loaded session = %+v", loaded)
	}
	project, err := reopened.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Sessions) != 1 || project.Sessions[0].ID != session.ID || !project.Sessions[0].UpdatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("project sessions = %+v", project.Sessions)
	}
}

func TestProjectSessionSummariesAreDerivedFromSessions(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-derived", "inspect", time.Now().UTC())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	project, err := store.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	project.Sessions = nil
	if err := store.SaveProjectIndex(project); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Sessions) != 1 || loaded.Sessions[0].ID != session.ID {
		t.Fatalf("derived sessions = %+v", loaded.Sessions)
	}
}

func TestConcurrentSessionWritesKeepEverySession(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	// 多个调用方共享同一个存储；提交串行化必须保留全部行。
	const count = 24
	errors := make(chan error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			session := domain.NewSession(sessionID(index), "inspect", time.Now().UTC())
			errors <- store.SaveSession(session)
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	project, err := store.LoadProject()
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Sessions) != count {
		t.Fatalf("session count = %d, want %d", len(project.Sessions), count)
	}
}

func TestCreateProjectStoreAtUsesExactRootAndSQLite(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".pentgo")
	store, err := CreateProjectStoreAt(root, "workspace", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Root() != root {
		t.Fatalf("root = %q, want %q", store.Root(), root)
	}
	if _, err := os.Stat(store.DatabasePath()); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteSchemaEnforcesIntegrity(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var integrity string
	if err := store.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}
	rows, err := store.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSessionCascadesTranscriptAndTurns(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-delete", "inspect", time.Now().UTC())
	turn, err := session.BeginTurn("turn-delete", "inspect", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.FinishTurn(turn.ID, "done", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: "inspect"}); err != nil {
		t.Fatal(err)
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSession(session.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("load deleted session error = %v", err)
	}
	var turns, messages int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM turns WHERE session_id = ?", session.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM transcript_messages WHERE session_id = ?", session.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if turns != 0 || messages != 0 {
		t.Fatalf("remaining turns/messages = %d/%d", turns, messages)
	}
}

func TestProjectFactsRoundTripFacts(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-facts", "inspect", time.Now().UTC())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	facts, err := store.OpenProjectFacts()
	if err != nil {
		t.Fatal(err)
	}
	defer facts.Close()
	updatedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	if err := facts.Upsert(context.Background(), ProjectFactWrite{Fact: domain.ProjectFact{
		FactKey: "host", Category: domain.FactCategoryNote, Summary: "TARGET", Body: "TARGET",
		Confidence: domain.FactConfidenceTentative, SourceSessionID: session.ID, CreatedAt: time.Now().UTC(), UpdatedAt: updatedAt,
	}}); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := facts.Get("host")
	if err != nil || !found {
		t.Fatalf("get = %#v found=%v err=%v", loaded, found, err)
	}
	if loaded.FactKey != "host" || loaded.SourceSessionID != session.ID || !loaded.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("project fact = %+v", loaded)
	}
}

func TestOpenProjectStoreRequiresSQLiteDatabase(t *testing.T) {
	root := t.TempDir()
	if _, err := OpenProjectStore(root); !errors.Is(err, ErrNotProject) {
		t.Fatalf("open error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pentgo.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database was created: %v", err)
	}
}

func newTestStore(t *testing.T) *ProjectStore {
	t.Helper()
	store, err := CreateProjectStore(t.TempDir(), "test project", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sessionID(index int) string {
	return "session-" + string(rune('a'+index/26)) + string(rune('a'+index%26))
}
