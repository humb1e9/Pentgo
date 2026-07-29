package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrWrite = errors.New("write evidence journal")

type Record struct {
	Seq        int       `json:"seq"`
	Tool       string    `json:"tool"`
	Arguments  any       `json:"arguments"`
	Success    bool      `json:"success"`
	Output     string    `json:"output"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Journal struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	next    int
	records map[int]Record
	secrets []string
	failed  error
	closed  bool
}

func NewJournal(path string, secrets ...string) (*Journal, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("evidence path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create evidence directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create evidence journal: %w", err)
	}
	return &Journal{
		file:    file,
		path:    path,
		records: make(map[int]Record),
		secrets: normalizeSecrets(secrets),
	}, nil
}

func (journal *Journal) Record(tool string, arguments any, success bool, output string, startedAt, finishedAt time.Time) (Record, error) {
	if journal == nil {
		return Record{}, fmt.Errorf("%w: nil journal", ErrWrite)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.failed != nil {
		return Record{}, journal.failed
	}
	if journal.closed {
		journal.failed = fmt.Errorf("%w: journal closed", ErrWrite)
		return Record{}, journal.failed
	}
	seq := journal.next + 1
	output = strings.TrimRight(journal.redact(output), "\n")
	if output != "" {
		output += "\n"
	}
	output += fmt.Sprintf("[evidence_ref: %d]", seq)
	record := Record{Seq: seq, Tool: tool, Arguments: arguments, Success: success, Output: output, StartedAt: startedAt, FinishedAt: finishedAt}
	data, err := json.Marshal(record)
	if err == nil {
		_, err = journal.file.Write(append(data, '\n'))
	}
	if err == nil {
		err = journal.file.Sync()
	}
	if err != nil {
		journal.failed = fmt.Errorf("%w: %v", ErrWrite, err)
		return Record{}, journal.failed
	}
	journal.next = seq
	journal.records[seq] = record
	return record, nil
}

func (journal *Journal) Lookup(seq int) (Record, bool) {
	if journal == nil {
		return Record{}, false
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	record, ok := journal.records[seq]
	return record, ok
}

func (journal *Journal) Path() string {
	if journal == nil {
		return ""
	}
	return journal.path
}

func (journal *Journal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return journal.failed
	}
	journal.closed = true
	if err := journal.file.Close(); err != nil && journal.failed == nil {
		journal.failed = fmt.Errorf("%w: close: %v", ErrWrite, err)
	}
	return journal.failed
}

func normalizeSecrets(values []string) []string {
	seen := make(map[string]bool)
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func (journal *Journal) redact(value string) string {
	for _, secret := range journal.secrets {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}
