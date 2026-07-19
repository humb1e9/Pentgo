package loop

import (
	"fmt"
	"strings"

	"pentgo/internal/runtime/exec"
)

const maxReportAuditBytes = 4 * 1024

// TurnValidation records the deterministic comparison between a turn's model
// declarations and its measured execution evidence.
type TurnValidation struct {
	Turn         int
	DeclaredMax  exec.EvidenceLevel
	EvidenceMax  exec.EvidenceLevel
	ClaimExceeds bool
	HasExecution bool
}

// ValidatedReportContext adds a deterministic audit summary to ReportContext.
type ValidatedReportContext struct {
	ReportContext
	TurnValidations         []TurnValidation
	ClaimsExceedingEvidence int
	TurnsWithExecution      int
}

// ValidateReportContext compares declared finding levels with evidence levels
// without performing I/O or making model calls.
func ValidateReportContext(context ReportContext) ValidatedReportContext {
	validations := make([]TurnValidation, 0, len(context.Turns))
	claimsExceedingEvidence := 0
	turnsWithExecution := 0

	for _, turn := range context.Turns {
		validation := TurnValidation{
			Turn:        turn.Number,
			DeclaredMax: exec.EvidenceInferred,
			EvidenceMax: exec.EvidenceInferred,
		}
		for _, block := range turn.Blocks {
			if block.Status == exec.ExecutionPreflightRejected {
				continue
			}
			validation.HasExecution = true
			if levelRank(block.Level) > levelRank(validation.EvidenceMax) {
				validation.EvidenceMax = block.Level
			}
		}
		if validation.HasExecution {
			turnsWithExecution++
		}
		for _, label := range turn.DeclaredLabels {
			if levelRank(label) > levelRank(validation.DeclaredMax) {
				validation.DeclaredMax = label
			}
		}
		if len(turn.DeclaredLabels) > 0 && levelRank(validation.DeclaredMax) > levelRank(validation.EvidenceMax) {
			validation.ClaimExceeds = true
			claimsExceedingEvidence++
		}
		validations = append(validations, validation)
	}

	return ValidatedReportContext{
		ReportContext:           context,
		TurnValidations:         validations,
		ClaimsExceedingEvidence: claimsExceedingEvidence,
		TurnsWithExecution:      turnsWithExecution,
	}
}

// PromptText preserves the report-context bound while reserving room for the
// audit summary so it remains visible on long engagements.
func (context ValidatedReportContext) PromptText() string {
	audit := context.auditText()
	baseLimit := maxReportContextBytes - len(audit)
	base := truncateReportContext(context.ReportContext.PromptText(), baseLimit)
	return base + audit
}

func (context ValidatedReportContext) auditText() string {
	var builder strings.Builder
	builder.WriteString("\n反幻觉审计:\n")
	summary := fmt.Sprintf(
		"超过证据的声明: %d 回合 / 总计 %d 回合\n有执行证据的回合: %d / %d\n",
		context.ClaimsExceedingEvidence,
		len(context.TurnValidations),
		context.TurnsWithExecution,
		len(context.TurnValidations),
	)
	detailLimit := maxReportAuditBytes - len(summary)
	for _, validation := range context.TurnValidations {
		if !validation.ClaimExceeds {
			continue
		}
		line := fmt.Sprintf("回合 %d: 模型声明 %s, 最高执行等级 %s - 声明超过证据\n", validation.Turn, validation.DeclaredMax, validation.EvidenceMax)
		if builder.Len()+len(line) > detailLimit {
			break
		}
		builder.WriteString(line)
	}
	builder.WriteString(summary)
	return builder.String()
}

func truncateReportContext(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= len(reportContextOmittedMarker) {
		return truncateBytes(value, limit)
	}
	return truncateBytes(value, limit-len(reportContextOmittedMarker)) + reportContextOmittedMarker
}

func levelRank(level exec.EvidenceLevel) int {
	switch level {
	case exec.EvidenceVerified:
		return 2
	case exec.EvidenceLikely:
		return 1
	default:
		return 0
	}
}
