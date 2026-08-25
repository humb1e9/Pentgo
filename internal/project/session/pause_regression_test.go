package session

import (
	"context"
	"testing"
	"time"
)

func TestWorkerPauseCancelsTurnAndKeepsWorkerUsable(t *testing.T) {
	session := NewSession("session-pause", "inspect", time.Now().UTC())
	started := make(chan struct{})
	calls := 0
	worker, err := NewWorker(context.Background(), session, func(ctx context.Context, current *Session, message string) error {
		turn, err := current.BeginTurn("", message, time.Now().UTC())
		if err != nil {
			return err
		}
		calls++
		if calls == 1 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return current.FinishTurn(turn.ID, "done", time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Stop()

	done := worker.Submit(context.Background(), "long task")
	<-started
	if !worker.Pause() {
		t.Fatal("Pause returned false for active turn")
	}
	if err := <-done; err == nil {
		t.Fatal("paused turn returned nil error")
	}
	if snapshot := worker.Snapshot(); snapshot.ActiveTurn == nil || snapshot.ActiveTurn.Status != TurnInterrupted || snapshot.Turns != 0 {
		t.Fatalf("paused snapshot = %#v", snapshot)
	}
	if err := <-worker.Submit(context.Background(), "next"); err != nil {
		t.Fatalf("worker was not usable after pause: %v", err)
	}
}
