package app

import (
	"context"
	"testing"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/domain"
)

func TestSnapshotDoesNotWaitForLongTurn(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	worker, err := NewSessionWorker(context.Background(), domain.NewSession("session-snapshot", "inspect", time.Now().UTC()), func(context.Context, *domain.Session, string) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done := worker.Submit(context.Background(), "long turn")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	// Snapshot 使用原子发布的数据；长时间模型回调独占可变状态时，
	// 它不能与 worker goroutine 发生竞争。
	snapshotDone := make(chan *domain.Session, 1)
	go func() { snapshotDone <- worker.Snapshot() }()
	select {
	case snapshot := <-snapshotDone:
		if snapshot == nil || snapshot.ID != "session-snapshot" {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("snapshot waited for the active turn")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	worker.Stop()
	<-worker.Done()
}

func TestProjectCloseStopsAllWorkers(t *testing.T) {
	store, err := storage.CreateProjectStore(t.TempDir(), "runtime", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenProjectRuntime(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTurnHandler(func(context.Context, *domain.Session, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	session, err := runtime.NewSession("inspect")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if snapshot := runtime.Snapshot(session.ID); snapshot == nil {
		t.Fatalf("session after close = %+v", snapshot)
	}
}

func TestProjectCloseInterruptsActiveTurnAndKeepsSession(t *testing.T) {
	store, err := storage.CreateProjectStore(t.TempDir(), "runtime", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenProjectRuntime(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	if err := runtime.SetTurnHandler(func(ctx context.Context, session *domain.Session, message string) error {
		_, err := session.BeginTurn("turn-close", message, time.Now().UTC())
		if err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	session, err := runtime.NewSession("inspect")
	if err != nil {
		t.Fatal(err)
	}
	done := runtime.Submit(context.Background(), session.ID, "long turn")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("interrupted turn returned nil")
	}
	reopened, err := storage.OpenProjectStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.LoadSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ActiveTurn == nil || persisted.ActiveTurn.Status != domain.TurnInterrupted {
		t.Fatalf("persisted session = %+v", persisted)
	}
}

func TestSessionUsesOriginalProjectRuntime(t *testing.T) {
	firstStore, err := storage.CreateProjectStore(t.TempDir(), "first", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := storage.CreateProjectStore(t.TempDir(), "second", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	first, err := OpenProjectRuntime(context.Background(), firstStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenProjectRuntime(context.Background(), secondStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	if err := first.SetTurnHandler(func(context.Context, *domain.Session, string) error { close(firstDone); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := second.SetTurnHandler(func(context.Context, *domain.Session, string) error { close(secondDone); return nil }); err != nil {
		t.Fatal(err)
	}
	firstSession, err := first.NewSession("first")
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := second.NewSession("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-first.Submit(context.Background(), firstSession.ID, "one"); err != nil {
		t.Fatal(err)
	}
	if err := <-second.Submit(context.Background(), secondSession.ID, "two"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first runtime handler did not run")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second runtime handler did not run")
	}
	if got := first.Transcript(firstSession.ID).Path(); got == second.Transcript(secondSession.ID).Path() {
		t.Fatalf("runtimes share transcript path %q", got)
	}
}

func TestNewSessionDoesNotPublishProjectOnCommitFailure(t *testing.T) {
	store, err := storage.CreateProjectStore(t.TempDir(), "runtime", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenProjectRuntime(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTurnHandler(func(context.Context, *domain.Session, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.NewSession("inspect"); err == nil {
		t.Fatal("new session succeeded after database close")
	}
	if project := runtime.Project(); project == nil || len(project.Sessions) != 0 {
		t.Fatalf("runtime project = %+v", project)
	}
	if sessions := runtime.Sessions(); len(sessions) != 0 {
		t.Fatalf("runtime sessions = %+v", sessions)
	}
}
