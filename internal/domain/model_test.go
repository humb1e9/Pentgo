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

func TestProjectFactValidation(t *testing.T) {
	for _, category := range []string{FactCategoryTarget, FactCategoryAuth, FactCategoryInfra, FactCategoryBusiness, FactCategoryFinding, FactCategoryChain, FactCategoryExploit, FactCategoryPOC, FactCategoryNote} {
		fact := ProjectFact{FactKey: "key", Category: category, Summary: "summary", Body: "body", Confidence: FactConfidenceTentative}
		if err := ValidateProjectFact(fact); err != nil {
			t.Fatalf("category %q rejected: %v", category, err)
		}
	}
	for _, fact := range []ProjectFact{
		{FactKey: "key", Category: "invalid", Summary: "summary", Body: "body", Confidence: FactConfidenceTentative},
		{FactKey: "key", Category: FactCategoryNote, Summary: "", Body: "body", Confidence: FactConfidenceTentative},
		{FactKey: "key", Category: FactCategoryNote, Summary: "summary", Body: "body", Confidence: FactConfidenceConfirmed},
		{FactKey: "key", Category: FactCategoryNote, Summary: "summary", Body: "body", Confidence: FactConfidenceTentative, EvidenceRefs: []int{1, 1}},
	} {
		if err := ValidateProjectFact(fact); err == nil {
			t.Fatalf("invalid fact accepted: %#v", fact)
		}
	}
}

func TestProjectFactEdgeValidation(t *testing.T) {
	edge := ProjectFactEdge{SourceFactKey: "source", TargetFactKey: "target", EdgeType: FactEdgeSupports, Confidence: FactConfidenceConfirmed}
	if err := ValidateProjectFactEdge(edge); err != nil {
		t.Fatal(err)
	}
	edge.EdgeType = "invalid"
	if err := ValidateProjectFactEdge(edge); err == nil {
		t.Fatal("invalid edge type accepted")
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
