package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutorRunsPythonAndShellInSourceOrder(t *testing.T) {
	workDir := t.TempDir()
	executor := NewExecutor(ExecutorConfig{WorkDir: workDir, Timeout: time.Second, MaxParallel: 2, MaxOutputBytes: 1024, LineRepeatLimit: 10, ScanLineRepeatLimit: 10})
	results := executor.Execute(context.Background(), ExecutionInput{
		SessionID: "eng-test",
		Target:    "https://example.com",
		Turn:      1,
		Blocks: []PreflightResult{
			approvedBlock(1, LanguagePython, "import os\nprint(os.environ['PENTGO_TARGET'])\n"),
			approvedBlock(2, LanguageShell, "echo shell-result\n"),
		},
	})
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Status != ExecutionSucceeded || results[0].Stdout != "https://example.com\n" || results[0].FinishedAt.IsZero() {
		t.Fatalf("python result = %+v", results[0])
	}
	if results[1].Status != ExecutionSucceeded || results[1].Stdout != "shell-result\n" {
		t.Fatalf("shell result = %+v", results[1])
	}
	if _, err := os.Stat(filepath.Join(workDir, "turn-001-block-001.py")); err != nil {
		t.Fatalf("python source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "turn-001-block-002.sh")); err != nil {
		t.Fatalf("shell source file: %v", err)
	}
}

func TestExecutorBoundsStoredOutput(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir(), Timeout: time.Second, MaxParallel: 1, MaxOutputBytes: 12, LineRepeatLimit: 10, ScanLineRepeatLimit: 10})
	results := executor.Execute(context.Background(), ExecutionInput{Turn: 1, Blocks: []PreflightResult{
		approvedBlock(1, LanguagePython, "print('abcdefghijklmnopqrstuvwxyz')\n"),
	}})
	if len(results) != 1 || results[0].Status != ExecutionSucceeded || !results[0].StdoutTruncated || len(results[0].Stdout) != 12 {
		t.Fatalf("result = %+v", results)
	}
}

func TestExecutorStopsTimedOutProcessGroup(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir(), Timeout: 50 * time.Millisecond, MaxParallel: 1, MaxOutputBytes: 1024, LineRepeatLimit: 10, ScanLineRepeatLimit: 10})
	started := time.Now()
	results := executor.Execute(context.Background(), ExecutionInput{Turn: 1, Blocks: []PreflightResult{
		approvedBlock(1, LanguageShell, "sleep 5\n"),
	}})
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("execution exceeded timeout bound: %v", elapsed)
	}
	if len(results) != 1 || results[0].Status != ExecutionTimedOut || !results[0].TimedOut {
		t.Fatalf("result = %+v", results)
	}
}

func TestExecutorStopsRepeatedOutput(t *testing.T) {
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir(), Timeout: time.Second, MaxParallel: 1, MaxOutputBytes: 1024, LineRepeatLimit: 3, ScanLineRepeatLimit: 10})
	results := executor.Execute(context.Background(), ExecutionInput{Turn: 1, Blocks: []PreflightResult{
		approvedBlock(1, LanguagePython, "while True:\n    print('same', flush=True)\n"),
	}})
	if len(results) != 1 || results[0].Status != ExecutionRepeatedOutput || !results[0].RepeatedOutput || strings.Count(results[0].Stdout, "same\n") < 3 {
		t.Fatalf("result = %+v", results)
	}
}

func TestExecutorWritesPerBlockEvidence(t *testing.T) {
	sink := &memoryEvidenceSink{}
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir(), Timeout: time.Second, MaxParallel: 1, MaxOutputBytes: 1024, LineRepeatLimit: 10, ScanLineRepeatLimit: 10, Evidence: sink})
	results := executor.Execute(context.Background(), ExecutionInput{Turn: 2, Blocks: []PreflightResult{
		approvedBlock(3, LanguagePython, "print('evidence')\n"),
	}})
	if len(results) != 1 || results[0].EvidencePath != "evidence/agent-turn-002-block-003.json" {
		t.Fatalf("result = %+v", results)
	}
	if sink.name != "agent-turn-002-block-003" || sink.value == nil {
		t.Fatalf("sink = %+v", sink)
	}
}

func TestExecutorGradesResultAndEvidence(t *testing.T) {
	captured := map[string]any{}
	sink := evidenceSinkFunc(func(name string, value any) (string, error) {
		captured[name] = value
		return "evidence/" + name + ".json", nil
	})
	executor := NewExecutor(ExecutorConfig{WorkDir: t.TempDir(), Timeout: 10 * time.Second, MaxParallel: 1, Evidence: sink})
	results := executor.Execute(context.Background(), ExecutionInput{
		Turn:   1,
		Blocks: []PreflightResult{{Approved: true, Code: "print('probe-ok')", Block: CodeBlock{Index: 1, Language: LanguagePython}}},
	})
	if len(results) != 1 || results[0].Level != EvidenceVerified {
		t.Fatalf("result level = %+v", results)
	}
	evidence, ok := captured["agent-turn-001-block-001"].(executionEvidence)
	if !ok || evidence.Level != EvidenceVerified {
		t.Fatalf("evidence level = %+v (ok=%v)", captured, ok)
	}
}

func TestExecutorResolvesRelativeWorkDirBeforeStartingChildProcess(t *testing.T) {
	t.Chdir(t.TempDir())
	executor := NewExecutor(ExecutorConfig{WorkDir: ".pentgo-test/work", Timeout: time.Second, MaxParallel: 1, MaxOutputBytes: 1024, LineRepeatLimit: 10, ScanLineRepeatLimit: 10})
	results := executor.Execute(context.Background(), ExecutionInput{Turn: 1, Blocks: []PreflightResult{
		approvedBlock(1, LanguageShell, "echo relative-workdir\n"),
	}})
	if len(results) != 1 || results[0].Status != ExecutionSucceeded || results[0].Stdout != "relative-workdir\n" {
		t.Fatalf("result = %+v", results)
	}
}

func approvedBlock(index int, language Language, code string) PreflightResult {
	return PreflightResult{
		Block:        CodeBlock{Index: index, Language: language, Code: code},
		OriginalCode: code,
		Code:         code,
		Approved:     true,
	}
}

type memoryEvidenceSink struct {
	name  string
	value any
}

type evidenceSinkFunc func(string, any) (string, error)

func (sink evidenceSinkFunc) WriteEvidence(name string, value any) (string, error) {
	return sink(name, value)
}

func (sink *memoryEvidenceSink) WriteEvidence(name string, value any) (string, error) {
	sink.name = name
	sink.value = value
	return "evidence/" + name + ".json", nil
}
