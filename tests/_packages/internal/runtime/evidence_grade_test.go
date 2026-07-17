package runtime

import (
	"strings"
	"testing"
)

func TestGradeEvidence(t *testing.T) {
	tests := []struct {
		name   string
		result ExecutionResult
		want   EvidenceLevel
	}{
		{"succeeded with stdout", ExecutionResult{Status: ExecutionSucceeded, Stdout: "200 OK\n"}, EvidenceVerified},
		{"succeeded stderr only", ExecutionResult{Status: ExecutionSucceeded, Stderr: "warning\n"}, EvidenceLikely},
		{"succeeded no output", ExecutionResult{Status: ExecutionSucceeded}, EvidenceInferred},
		{"failed with output", ExecutionResult{Status: ExecutionFailed, Stdout: "500\n"}, EvidenceLikely},
		{"failed no output", ExecutionResult{Status: ExecutionFailed}, EvidenceInferred},
		{"timed out with stderr", ExecutionResult{Status: ExecutionTimedOut, Stderr: "timeout\n"}, EvidenceLikely},
		{"repeated output", ExecutionResult{Status: ExecutionRepeatedOutput, Stdout: "loop\n"}, EvidenceLikely},
		{"cancelled with output", ExecutionResult{Status: ExecutionCancelled, Stdout: "partial\n"}, EvidenceInferred},
		{"preflight rejected", ExecutionResult{Status: ExecutionPreflightRejected}, EvidenceInferred},
		{"whitespace-only stdout", ExecutionResult{Status: ExecutionSucceeded, Stdout: "   \n"}, EvidenceInferred},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := GradeEvidence(test.result); got != test.want {
				t.Fatalf("GradeEvidence(%s) = %s, want %s", test.name, got, test.want)
			}
		})
	}
}

func TestEvidenceLevelsMatchPromptVocabulary(t *testing.T) {
	prompt := basePromptContent()
	for _, level := range []EvidenceLevel{EvidenceVerified, EvidenceLikely, EvidenceInferred} {
		if !strings.Contains(prompt, string(level)) {
			t.Fatalf("prompt missing evidence level %q", level)
		}
	}
}
