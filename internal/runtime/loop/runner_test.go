package loop

import (
	"pentgo/internal/runtime/exec"
	"strings"
	"testing"
)

func TestRenderExecutionResultsHasNoLegacyEvidencePath(t *testing.T) {
	output := RenderExecutionResults(1, []exec.ExecutionResult{{Block: exec.CodeBlock{Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "RESULT\n"}})
	if !strings.Contains(output, "RESULT") || strings.Contains(output, "evidence:") {
		t.Fatalf("output = %q", output)
	}
}
