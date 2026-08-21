package storage

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestEvidenceStorePersistsRedactedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pentgo.db")
	store, err := OpenEvidenceStore(path, "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.RecordResult(context.Background(), "fixture_probe", map[string]any{"target": "TARGET"}, true, "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != 1 || record.Output != "[redacted]\n[evidence_ref: 1]" {
		t.Fatalf("record = %+v", record)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenEvidenceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, ok := reopened.Lookup(record.Seq)
	if !ok || loaded.Output != record.Output {
		t.Fatalf("loaded record = %+v, %v", loaded, ok)
	}
}

func TestEvidenceStoreReopenContinuesSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pentgo.db")
	first, err := OpenEvidenceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.RecordResult(context.Background(), "first", map[string]any{}, true, "one"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenEvidenceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	record, err := second.RecordResult(context.Background(), "second", map[string]any{}, true, "two")
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != 2 {
		t.Fatalf("next sequence = %d, want 2", record.Seq)
	}
}

func TestEvidenceStoreConcurrentRecordsHaveUniqueSequences(t *testing.T) {
	store, err := OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// 并发工具调用仍须各自获得且仅获得一次证据引用。
	const count = 24
	sequences := make(chan int, count)
	var wait sync.WaitGroup
	wait.Add(count)
	for index := 0; index < count; index++ {
		go func(index int) {
			defer wait.Done()
			record, err := store.RecordResult(context.Background(), "fixture", map[string]any{"index": index}, true, "RESULT")
			if err != nil {
				t.Errorf("record %d: %v", index, err)
				return
			}
			sequences <- record.Seq
		}(index)
	}
	wait.Wait()
	close(sequences)
	seen := make(map[int]bool, count)
	for sequence := range sequences {
		seen[sequence] = true
	}
	if len(seen) != count {
		t.Fatalf("sequence count = %d, want %d", len(seen), count)
	}
	for sequence := 1; sequence <= count; sequence++ {
		if !seen[sequence] {
			t.Fatalf("missing sequence %d", sequence)
		}
	}
}

func TestEvidenceStoreFailureIsSticky(t *testing.T) {
	store, err := OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	// 强制触发底层数据库失败；后续写入必须返回这一持续性失败，
	// 不能产生表面可用但顺序混乱的 journal。
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, first := store.RecordResult(context.Background(), "fixture", map[string]any{}, true, "ok")
	_, second := store.RecordResult(context.Background(), "fixture", map[string]any{}, true, "ok")
	if !errors.Is(first, ErrWrite) || !errors.Is(second, ErrWrite) {
		t.Fatalf("errors = %v, %v", first, second)
	}
}
