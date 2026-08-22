package domain

import (
	"testing"
	"time"
)

func TestSessionRetainsCompletedTurn(t *testing.T) {
	session := NewSession("session-1", "inspect", time.Now().UTC())
	if !session.AddTargets("https://TARGET") {
		t.Fatal("expected target to be added")
	}
	turn, err := session.BeginTurn("turn-1", "inspect TARGET", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.FinishTurn(turn.ID, "summary", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if session.Turns != 1 || session.FinalSummary != "summary" {
		t.Fatalf("session = %+v", session)
	}
	if session.ActiveTurn == nil || session.ActiveTurn.Status != TurnDone {
		t.Fatalf("active turn = %+v", session.ActiveTurn)
	}
}

func TestNewSessionDerivesName(t *testing.T) {
	session := NewSession("session-1", "inspect TARGET", time.Now().UTC())
	if session.Name != session.ID {
		t.Fatalf("name = %q", session.Name)
	}
	if unnamed := NewSession("", "", time.Now().UTC()); unnamed.Name != unnamed.ID {
		t.Fatalf("default name/id = %q/%q", unnamed.Name, unnamed.ID)
	}
}

func TestBlackboardReplacementRefreshesFactUpdatedAt(t *testing.T) {
	first := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	board := &Blackboard{}
	if err := board.NoteFact(Fact{Key: "target", Value: "first", At: first, UpdatedAt: first}); err != nil {
		t.Fatal(err)
	}
	if err := board.NoteFact(Fact{Key: "target", Value: "replacement", At: first}); err != nil {
		t.Fatal(err)
	}
	fact := board.Facts[0]
	if fact.Value != "replacement" || !fact.UpdatedAt.After(first) {
		t.Fatalf("fact = %#v", fact)
	}
}

func TestTurnStatusTransitions(t *testing.T) {
	session := NewSession("session-1", "inspect", time.Now().UTC())
	turn, err := session.BeginTurn("turn-1", "inspect", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.InterruptTurn(turn.ID, "cancelled", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if session.ActiveTurn.Status != TurnInterrupted {
		t.Fatalf("session = %+v", session)
	}
	if _, err := session.BeginTurn("turn-2", "again", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
