package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion 记录每个项目数据库应使用的 SQLite 布局版本。
const schemaVersion = 4

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

CREATE TABLE IF NOT EXISTS facts (
    key TEXT PRIMARY KEY,
    position INTEGER NOT NULL UNIQUE CHECK (position >= 0),
    value TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS transcript_messages (
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

CREATE TABLE IF NOT EXISTS transcript_tool_calls (
    session_id TEXT NOT NULL,
    message_seq INTEGER NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    arguments_json TEXT,
    raw_arguments TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, message_seq, position),
    FOREIGN KEY (session_id, message_seq)
        REFERENCES transcript_messages(session_id, seq) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS context_surface_nodes (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    position INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('source', 'checkpoint', 'pruned_tool')),
    source_start_seq INTEGER NOT NULL,
    source_end_seq INTEGER NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL,
    PRIMARY KEY(session_id, id),
    UNIQUE(session_id, position)
);

CREATE TABLE IF NOT EXISTS context_surface_state (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS context_compactions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL,
    source_start_seq INTEGER NOT NULL,
    source_end_seq INTEGER NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('started', 'committed', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    finished_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS context_compactions_one_started_range
    ON context_compactions(session_id, generation, source_start_seq, source_end_seq)
    WHERE status = 'started';
`

// openSQLite 为项目数据库配置 WAL 持久性、外键和私有所有者权限，
// 然后创建或校验当前 Schema。
func openSQLite(path string) (*sql.DB, error) {
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)",
	}).String()
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
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read sqlite schema version: %w", err)
	}
	if version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if err := migrateSQLite(db, version); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite schema version: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set database permissions: %w", err)
	}
	return db, nil
}

// migrateSQLite 删除已废弃的数据结构，并保留可恢复的项目数据。
func migrateSQLite(db *sql.DB, version int) error {
	if version >= schemaVersion {
		return nil
	}
	if version == 1 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin sqlite schema v1 to v2 migration: %w", err)
		}
		if _, err := tx.Exec("DROP TABLE IF EXISTS finding_evidence"); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate sqlite schema v1 to v2: %w", err)
		}
		if _, err := tx.Exec("DROP TABLE IF EXISTS findings"); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate sqlite schema v1 to v2: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit sqlite schema v1 to v2 migration: %w", err)
		}
	}
	if version >= 1 && version <= 2 {
		if _, err := db.Exec("ALTER TABLE transcript_messages ADD COLUMN reasoning_content TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migrate sqlite schema v2 to v3: %w", err)
		}
	}
	if version >= 1 && version <= 3 {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin sqlite schema v3 to v4 migration: %w", err)
		}
		if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS context_surface_nodes (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    position INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('source', 'checkpoint', 'pruned_tool')),
    source_start_seq INTEGER NOT NULL,
    source_end_seq INTEGER NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL,
    PRIMARY KEY(session_id, id),
    UNIQUE(session_id, position)
);
CREATE TABLE IF NOT EXISTS context_surface_state (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS context_compactions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL,
    source_start_seq INTEGER NOT NULL,
    source_end_seq INTEGER NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('started', 'committed', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    finished_at INTEGER
);`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate sqlite schema v3 to v4: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit sqlite schema v3 to v4 migration: %w", err)
		}
	}
	return nil
}

// timeValue 将 UTC 时间戳存储为 Unix 纳秒，以获得可排序精度。
func timeValue(value time.Time) int64 {
	return value.UTC().UnixNano()
}

// parseTime 将 SQLite 的纳秒时间戳表示转换回 UTC。
func parseTime(value int64) time.Time {
	return time.Unix(0, value).UTC()
}

// nullableText 将空的可选文本保留为 SQL NULL。
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// boolInt 将 Go 布尔值映射为 SQLite 表使用的整数表示。
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
