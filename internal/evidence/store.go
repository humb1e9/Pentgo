package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"pentgo/internal/storage"
)

// ErrWrite 包装首次持久化 journal 写入失败，避免存储进入失败状态后，
// 后续写入仍显示为成功。
var ErrWrite = errors.New("write evidence journal")

// Record 是一条持久化工具执行结果，以递增序号标识；该序号用作模型可见输出中的证据引用。
type Record struct {
	Seq        int       `json:"seq"`
	Tool       string    `json:"tool"`
	Arguments  any       `json:"arguments"`
	Success    bool      `json:"success"`
	Output     string    `json:"output"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// EvidenceStore 串行化审计 journal 写入，并在提交或返回模型前脱敏配置的敏感值。
type EvidenceStore struct {
	mu      sync.Mutex
	db      *sql.DB
	secrets []string
	failed  error
	closed  bool
}

// OpenEvidenceStore 打开 path 指向的 SQLite 数据库中的证据关系。
func OpenEvidenceStore(path string, secrets ...string) (*EvidenceStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("evidence database path is empty")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	return &EvidenceStore{db: db, secrets: normalizeSecrets(secrets)}, nil
}

// RecordResult 返回提交到数据库的准确脱敏输出。
func (store *EvidenceStore) RecordResult(ctx context.Context, tool string, arguments map[string]any, success bool, output string) (Record, error) {
	if store == nil || store.db == nil {
		return Record{}, fmt.Errorf("evidence store is nil")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return Record{}, ctx.Err()
		default:
		}
	}
	now := time.Now().UTC()
	return store.appendRecord(tool, arguments, success, output, now, now)
}

// appendRecord 在同一事务中分配序号并追加 evidence_ref 标记，
// 确保持久化值与模型可见值一致。
func (store *EvidenceStore) appendRecord(tool string, arguments any, success bool, output string, startedAt, finishedAt time.Time) (Record, error) {
	if store == nil || store.db == nil {
		return Record{}, fmt.Errorf("%w: nil journal", ErrWrite)
	}
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return Record{}, fmt.Errorf("%w: encode arguments: %v", ErrWrite, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failed != nil {
		return Record{}, store.failed
	}
	if store.closed {
		store.failed = fmt.Errorf("%w: journal closed", ErrWrite)
		return Record{}, store.failed
	}
	tx, err := store.db.Begin()
	if err != nil {
		return Record{}, store.fail(err)
	}
	baseOutput := strings.TrimRight(store.redact(output), "\n")
	result, err := tx.Exec(`
		INSERT INTO evidence_records(tool, arguments_json, success, output, started_at, finished_at)
		VALUES(?, ?, ?, '', ?, ?)`,
		tool, string(argumentsJSON), boolInt(success), timeValue(startedAt), timeValue(finishedAt),
	)
	if err != nil {
		_ = tx.Rollback()
		return Record{}, store.fail(err)
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return Record{}, store.fail(err)
	}
	persistedOutput := baseOutput
	if persistedOutput != "" {
		persistedOutput += "\n"
	}
	persistedOutput += fmt.Sprintf("[evidence_ref: %d]", sequence)
	if _, err := tx.Exec("UPDATE evidence_records SET output = ? WHERE seq = ?", persistedOutput, sequence); err != nil {
		_ = tx.Rollback()
		return Record{}, store.fail(err)
	}
	if err := tx.Commit(); err != nil {
		return Record{}, store.fail(err)
	}
	return Record{
		Seq: int(sequence), Tool: tool, Arguments: arguments, Success: success,
		Output: persistedOutput, StartedAt: startedAt.UTC(), FinishedAt: finishedAt.UTC(),
	}, nil
}

// fail 将写入失败设为持续状态，因为此后证据顺序不再可信。
func (store *EvidenceStore) fail(err error) error {
	store.failed = fmt.Errorf("%w: %v", ErrWrite, err)
	return store.failed
}

// Exists reports whether an Evidence record exists. It intentionally does not
// expose content or interpret success, because fact references are provenance
// links rather than confidence claims.
func (store *EvidenceStore) Exists(sequence int) bool {
	if store == nil || store.db == nil || sequence <= 0 {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return false
	}
	var found bool
	return store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM evidence_records WHERE seq = ?)`, sequence).Scan(&found) == nil && found
}

// Close 释放数据库并返回首个持续性写入失败。
func (store *EvidenceStore) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return store.failed
	}
	store.closed = true
	if err := store.db.Close(); err != nil && store.failed == nil {
		store.failed = fmt.Errorf("%w: close: %v", ErrWrite, err)
	}
	return store.failed
}

// normalizeSecrets 对值去重，并优先替换较长值，避免短敏感值仅部分遮蔽较长敏感值。
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

// redact 在输出到达 SQLite 或 LLM 前替换全部配置的敏感值。
func (store *EvidenceStore) redact(value string) string {
	for _, secret := range store.secrets {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}

func timeValue(value time.Time) int64 { return value.UTC().UnixNano() }
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
