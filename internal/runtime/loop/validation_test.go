package loop

import (
	"strings"
	"testing"

	"pentgo/internal/runtime/exec"
)


func TestValidateReportContextNoTurns(t *testing.T) {
	rc := ReportContext{Target: "https://example.test", Intent: "test"}
	validated := ValidateReportContext(rc)
	if validated.ClaimsExceedingEvidence != 0 || validated.TurnsWithExecution != 0 {
		t.Fatalf("empty context = %+v", validated)
	}
	if validated.Target != rc.Target {
		t.Fatal("embedded report context was not preserved")
	}
}

func TestValidateReportContextClaimExceedsEvidence(t *testing.T) {
	rc := ReportContext{Turns: []ReportTurn{{
		Number:         1,
		DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceVerified},
		Blocks: []ReportBlock{{
			Level:  exec.EvidenceInferred,
			Status: exec.ExecutionFailed,
		}},
	}}}

	validated := ValidateReportContext(rc)
	if len(validated.TurnValidations) != 1 {
		t.Fatalf("TurnValidations len = %d", len(validated.TurnValidations))
	}
	turn := validated.TurnValidations[0]
	if !turn.ClaimExceeds {
		t.Fatal("ClaimExceeds = false, want true")
	}
	if turn.DeclaredMax != exec.EvidenceVerified || turn.EvidenceMax != exec.EvidenceInferred {
		t.Fatalf("DeclaredMax/EvidenceMax = %s/%s", turn.DeclaredMax, turn.EvidenceMax)
	}
	if validated.ClaimsExceedingEvidence != 1 || validated.TurnsWithExecution != 1 {
		t.Fatalf("validation counts = %+v", validated)
	}
}

func TestValidateReportContextSupportedClaim(t *testing.T) {
	rc := ReportContext{Turns: []ReportTurn{{
		Number:         2,
		DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceLikely},
		Blocks: []ReportBlock{{
			Level:  exec.EvidenceVerified,
			Status: exec.ExecutionSucceeded,
		}},
	}}}

	validated := ValidateReportContext(rc)
	if validated.ClaimsExceedingEvidence != 0 {
		t.Fatalf("ClaimsExceedingEvidence = %d, want 0", validated.ClaimsExceedingEvidence)
	}
	if !validated.TurnValidations[0].HasExecution {
		t.Fatal("HasExecution = false")
	}
}

func TestValidateReportContextNoDeclaredLabels(t *testing.T) {
	rc := ReportContext{Turns: []ReportTurn{{
		Number: 3,
		Blocks: []ReportBlock{{
			Level:  exec.EvidenceVerified,
			Status: exec.ExecutionSucceeded,
		}},
	}}}

	validated := ValidateReportContext(rc)
	if validated.ClaimsExceedingEvidence != 0 || validated.TurnValidations[0].ClaimExceeds {
		t.Fatalf("validation = %+v", validated)
	}
}

func TestValidateReportContextTurnWithoutBlocks(t *testing.T) {
	rc := ReportContext{Turns: []ReportTurn{{
		Number:         4,
		DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceVerified},
	}}}

	validated := ValidateReportContext(rc)
	turn := validated.TurnValidations[0]
	if !turn.ClaimExceeds {
		t.Fatal("VERIFIED claim without blocks should exceed evidence")
	}
	if turn.HasExecution {
		t.Fatal("HasExecution = true without blocks")
	}
}

func TestValidateReportContextPreflightRejectionIsNotExecution(t *testing.T) {
	rc := ReportContext{Turns: []ReportTurn{{
		Number:         5,
		DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceInferred},
		Blocks: []ReportBlock{{
			Level:  exec.EvidenceInferred,
			Status: exec.ExecutionPreflightRejected,
		}},
	}}}

	validated := ValidateReportContext(rc)
	turn := validated.TurnValidations[0]
	if turn.HasExecution || validated.TurnsWithExecution != 0 {
		t.Fatalf("preflight-rejected block counted as execution: %+v", validated)
	}
}

func TestValidatedReportContextPromptTextIncludesAuditSection(t *testing.T) {
	rc := ReportContext{
		Target: "https://example.test",
		Turns: []ReportTurn{{
			Number:         1,
			DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceVerified},
			Blocks: []ReportBlock{{
				Level:  exec.EvidenceInferred,
				Status: exec.ExecutionFailed,
			}},
		}},
	}

	text := ValidateReportContext(rc).PromptText()
	for _, want := range []string{"https://example.test", "反幻觉审计", "声明超过证据"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PromptText missing %q: %q", want, text)
		}
	}
}

func TestValidatedReportContextPromptTextKeepsAuditWithinLimit(t *testing.T) {
	turns := make([]ReportTurn, 0, 300)
	for number := 1; number <= 300; number++ {
		turns = append(turns, ReportTurn{
			Number:         number,
			Decision:       strings.Repeat("execution summary ", 80),
			DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceVerified},
			Blocks: []ReportBlock{{
				Level:  exec.EvidenceInferred,
				Status: exec.ExecutionFailed,
			}},
		})
	}

	text := ValidateReportContext(ReportContext{Target: "https://example.test", Turns: turns}).PromptText()
	if len(text) > maxReportContextBytes {
		t.Fatalf("PromptText len = %d, limit = %d", len(text), maxReportContextBytes)
	}
	if !strings.Contains(text, "反幻觉审计") || !strings.Contains(text, "超过证据的声明: 300 回合") {
		t.Fatalf("PromptText omitted audit summary: %q", text)
	}
}
