package evidence

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJournalCreatesEmptyFileAndAppendsOneCompactLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	journal, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("new journal = %q, want empty", before)
	}
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)
	record, err := journal.Record("execute_python", map[string]any{"script": "print('RESULT')"}, true, "RESULT", started, finished)
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != 1 || record.Output != "RESULT\n[evidence_ref: 1]" {
		t.Fatalf("record = %+v", record)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("journal must contain one physical JSONL line: %q", data)
	}
	var decoded Record
	if err := json.Unmarshal(data[:len(data)-1], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Output != record.Output || decoded.Tool != "execute_python" || !decoded.Success {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestJournalRedactsConfiguredSecretsAndIndexesSuccess(t *testing.T) {
	journal, err := NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"), "TOKEN-VALUE")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	record, err := journal.Record("fixture_echo", map[string]any{"value": "TARGET"}, true, "TOKEN-VALUE", time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record.Output, "TOKEN-VALUE") || !strings.Contains(record.Output, "[redacted]") {
		t.Fatalf("output = %q", record.Output)
	}
	stored, ok := journal.Lookup(record.Seq)
	if !ok || !stored.Success || stored.Output != record.Output {
		t.Fatalf("lookup = %+v, %v", stored, ok)
	}
	if _, ok := journal.Lookup(99); ok {
		t.Fatal("unexpected missing reference")
	}
}

func TestJournalConcurrentAppendsUseCompletionOrderSequenceAndValidLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	journal, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	const calls = 32
	var wait sync.WaitGroup
	wait.Add(calls)
	for index := 0; index < calls; index++ {
		go func(index int) {
			defer wait.Done()
			_, recordErr := journal.Record("exec", map[string]any{"command": index}, index%2 == 0, "RESULT", time.Now().UTC(), time.Now().UTC())
			if recordErr != nil {
				t.Errorf("record %d: %v", index, recordErr)
			}
		}(index)
	}
	wait.Wait()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	seen := make(map[int]bool, calls)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		seen[record.Seq] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= calls; seq++ {
		if !seen[seq] {
			t.Fatalf("missing sequence %d", seq)
		}
	}
}

func TestJournalFailureIsStickyAndClassified(t *testing.T) {
	journal, err := NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.file.Close(); err != nil {
		t.Fatal(err)
	}
	_, first := journal.Record("exec", map[string]any{"command": "true"}, true, "ok", time.Now().UTC(), time.Now().UTC())
	_, second := journal.Record("exec", map[string]any{"command": "true"}, true, "ok", time.Now().UTC(), time.Now().UTC())
	if !errors.Is(first, ErrWrite) || !errors.Is(second, ErrWrite) {
		t.Fatalf("errors = %v, %v", first, second)
	}
}
