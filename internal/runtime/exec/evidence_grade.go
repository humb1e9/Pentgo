package exec

import "strings"

// EvidenceLevel 描述一个代码块执行结果作为证据的确定性等级。
type EvidenceLevel string

const (
	// EvidenceVerified 表示干净成功且有可观测的程序 stdout。
	EvidenceVerified EvidenceLevel = "VERIFIED"
	// EvidenceLikely 表示有输出但非干净成功，或成功但仅有诊断 stderr。
	EvidenceLikely EvidenceLevel = "LIKELY"
	// EvidenceInferred 表示无可观测输出、被取消或未执行。
	EvidenceInferred EvidenceLevel = "INFERRED"
)

// GradeEvidence 依据执行状态与输出确定性地为结果分级。
func GradeEvidence(result ExecutionResult) EvidenceLevel {
	hasStdout := strings.TrimSpace(result.Stdout) != ""
	hasStderr := strings.TrimSpace(result.Stderr) != ""
	switch result.Status {
	case ExecutionSucceeded:
		if hasStdout {
			return EvidenceVerified
		}
		if hasStderr {
			return EvidenceLikely
		}
		return EvidenceInferred
	case ExecutionFailed, ExecutionTimedOut, ExecutionRepeatedOutput:
		if hasStdout || hasStderr {
			return EvidenceLikely
		}
		return EvidenceInferred
	default:
		return EvidenceInferred
	}
}
