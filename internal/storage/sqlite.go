package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite"
)

// schemaVersion 记录每个项目数据库应使用的 SQLite 布局版本。
const schemaVersion = 11

// schema 由 openSQLite 在单个事务中应用于新数据库。
// 表使用规范化关系持久化事实，而非序列化状态块。
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    intent TEXT NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0 CHECK (turn_count >= 0),
    last_turn_id TEXT REFERENCES turns(id) DEFERRABLE INITIALLY DEFERRED,
    active_turn_id TEXT REFERENCES turns(id) DEFERRABLE INITIALLY DEFERRED,
    final_summary TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_updated_at ON sessions(updated_at DESC, id);

CREATE TABLE IF NOT EXISTS session_targets (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    target TEXT NOT NULL,
    PRIMARY KEY (session_id, position),
    UNIQUE (session_id, target)
);

CREATE TABLE IF NOT EXISTS turns (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('running', 'done', 'interrupted', 'failed')),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS turns_session_started ON turns(session_id, started_at, id);

CREATE TABLE IF NOT EXISTS evidence_records (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    tool TEXT NOT NULL,
    arguments_json TEXT NOT NULL,
    success INTEGER NOT NULL CHECK (success IN (0, 1)),
    output TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS project_facts (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fact_key TEXT NOT NULL,
    value TEXT NOT NULL,
    evidence_ref INTEGER NULL REFERENCES evidence_records(seq),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(project_id, fact_key)
);

CREATE TABLE IF NOT EXISTS conversation_messages (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL CHECK (seq > 0),
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    reasoning_content TEXT NOT NULL DEFAULT '',
    tool_call_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    tool_arguments_json TEXT,
    PRIMARY KEY (session_id, seq)
);

CREATE TABLE IF NOT EXISTS conversation_tool_calls (
    session_id TEXT NOT NULL,
    message_seq INTEGER NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    arguments_json TEXT,
    raw_arguments TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, message_seq, position),
    FOREIGN KEY (session_id, message_seq)
        REFERENCES conversation_messages(session_id, seq) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS runner_checkpoints (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL,
    checkpoint_id TEXT NOT NULL,
    payload BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(session_id, turn_id, checkpoint_id)
);

CREATE TABLE IF NOT EXISTS context_summaries (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    covered_through_seq INTEGER NOT NULL CHECK(covered_through_seq > 0),
    content TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS project_notices (
    notice_key TEXT PRIMARY KEY
);
`

// openSQLite creates or validates the current schema. Existing incompatible
// databases are discarded because project state is intentionally disposable.
func OpenSQLite(path string) (*sql.DB, error) {
	open := func() (*sql.DB, error) {
		dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"}).String()
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("open sqlite database: %w", err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("connect sqlite database: %w", err)
		}
		if _, err := db.Exec(schema); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize sqlite schema: %w", err)
		}
		if err := migrateLegacyCheckpoints(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := removeLegacyContextSurface(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := validateCurrentSchema(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
		return db, nil
	}
	db, err := open()
	if err == nil {
		return db, nil
	}
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	return open()
}

func migrateLegacyCheckpoints(db *sql.DB) error {
	var legacyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'adk_checkpoints'`).Scan(&legacyCount); err != nil {
		return fmt.Errorf("check legacy checkpoints: %w", err)
	}
	if legacyCount == 0 {
		return nil
	}
	transaction, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin checkpoint migration: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`INSERT OR IGNORE INTO runner_checkpoints(session_id, turn_id, checkpoint_id, payload, updated_at) SELECT session_id, turn_id, checkpoint_id, payload, updated_at FROM adk_checkpoints`); err != nil {
		return fmt.Errorf("copy legacy checkpoints: %w", err)
	}
	if _, err := transaction.Exec(`DROP TABLE adk_checkpoints`); err != nil {
		return fmt.Errorf("remove legacy checkpoints: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit checkpoint migration: %w", err)
	}
	return nil
}

// removeLegacyContextSurface drops the retired node graph. The conversation
// ledger remains intact and is used to rebuild rolling summaries.
func removeLegacyContextSurface(db *sql.DB) error {
	for _, table := range []string{"context_compactions", "context_surface_nodes", "context_surface_state"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			return fmt.Errorf("remove legacy context surface table %s: %w", table, err)
		}
	}
	return nil
}

func validateCurrentSchema(queryer interface {
	QueryRow(string, ...any) *sql.Row
}) error {
	var reasoningColumns int
	if err := queryer.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'reasoning_content'`).Scan(&reasoningColumns); err != nil || reasoningColumns != 1 {
		return fmt.Errorf("current schema check failed")
	}
	var legacyFacts int
	if err := queryer.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'facts'`).Scan(&legacyFacts); err != nil || legacyFacts != 0 {
		return fmt.Errorf("current schema check failed")
	}
	return nil
}
