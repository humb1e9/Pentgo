package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func approved(index int, language Language, code string) PreflightResult {
	return PreflightResult{Block: CodeBlock{Index: index, Language: language, Code: code}, OriginalCode: code, Code: code, Approved: true}
}
func TestExecutorRunsAndCleansRegisteredScripts(t *testing.T) {
	work := t.TempDir()
	executor := NewExecutor(ExecutorConfig{WorkDir: work, Timeout: time.Second})
	results := executor.Execute(context.Background(), ExecutionInput{Target: "TARGET", Turn: 1, Blocks: []PreflightResult{approved(1, LanguageShell, "echo RESULT\n"), approved(2, LanguagePython, "from pathlib import Path\nPath('artifact.txt').write_text('keep')\n")}})
	if len(results) != 2 || results[0].Status != ExecutionSucceeded || results[0].Stdout != "RESULT\n" || results[1].Status != ExecutionSucceeded {
		t.Fatalf("results = %+v", results)
	}
	if err := executor.CleanupGeneratedScripts(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(results[0].ScriptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime script = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(work, "artifact.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("artifact = %q, %v", data, err)
	}
}
func TestExecutorPreflightRejects(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir()})
	results := executor.Execute(context.Background(), ExecutionInput{Blocks: []PreflightResult{{Block: CodeBlock{Language: LanguagePython}, Rejection: "rejected"}}})
	if len(results) != 1 || results[0].Status != ExecutionPreflightRejected {
		t.Fatalf("results = %+v", results)
	}
}
