package project

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sessionstate "pentgo/internal/session"
)

// ErrNotProject 将缺失或格式错误的工作区与普通 SQLite 打开错误区分开，
// 使 Coordinator 能在需要时创建新项目。
var ErrNotProject = errors.New("not a pentgo project")

// ProjectStore 持有项目数据库，并串行化多表提交。
type ProjectStore struct {
	root     string
	db       *sql.DB
	commitMu sync.Mutex
	closeMu  sync.Mutex
	closed   bool
}

// CreateProjectStore 在 parent 下创建唯一命名的项目目录。
func CreateProjectStore(parent, name string, now time.Time) (*ProjectStore, error) {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return nil, fmt.Errorf("project parent directory is empty")
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return nil, fmt.Errorf("resolve project parent directory: %w", err)
	}
	if isProjectRoot(parent) {
		return nil, fmt.Errorf("project parent is already a project")
	}
	root := filepath.Join(parent, newID("project"))
	return createProjectStore(root, name, filepath.Base(root), now)
}

// CreateProjectStoreAt 在指定工作区目录初始化项目。
func CreateProjectStoreAt(root, name string, now time.Time) (*ProjectStore, error) {
	return createProjectStore(root, name, newID("project"), now)
}

// createProjectStore 创建私有临时存储、初始化 SQLite，
// 并在暴露可用存储前写入唯一项目行。
func createProjectStore(root, name, projectID string, now time.Time) (*ProjectStore, error) {
	root = strings.TrimSpace(root)
	name = strings.TrimSpace(name)
	if root == "" {
		return nil, fmt.Errorf("project root directory is empty")
	}
	if name == "" {
		return nil, fmt.Errorf("project name is empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project root directory: %w", err)
	}
	if isProjectRoot(root) {
		return nil, fmt.Errorf("project root is already a project")
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("create project directory: %w", err)
	}
	db, err := OpenSQLite(filepath.Join(root, "pentgo.db"))
	if err != nil {
		return nil, err
	}
	store := &ProjectStore{root: root, db: db}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	project := &Project{ID: strings.TrimSpace(projectID), Name: name, CreatedAt: now, UpdatedAt: now}
	if err := store.saveProject(project); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// OpenProjectStore 校验项目数据库并恢复其私有 tmp 目录。
func OpenProjectStore(root string) (*ProjectStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("%w: project directory is empty", ErrNotProject)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	databasePath := filepath.Join(root, "pentgo.db")
	_, databaseErr := os.Stat(databasePath)
	if errors.Is(databaseErr, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotProject, root)
	}
	if databaseErr != nil && !errors.Is(databaseErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect project database: %w", databaseErr)
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("create project temporary directory: %w", err)
	}
	db, err := OpenSQLite(databasePath)
	if err != nil {
		return nil, err
	}
	store := &ProjectStore{root: root, db: db}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("inspect project database: %w", err)
	}
	if count == 0 {
		_ = store.Close()
		return nil, fmt.Errorf("%w: project database has no project row", ErrNotProject)
	}
	return store, nil
}

// isProjectRoot 执行初始化前使用的轻量级标记检查。
func isProjectRoot(root string) bool {
	_, err := os.Stat(filepath.Join(root, "pentgo.db"))
	return err == nil
}

// Root 返回包含 pentgo.db 和项目 tmp 目录的路径。
func (store *ProjectStore) Root() string {
	if store == nil {
		return ""
	}
	return store.root
}

// WorkspaceRoot 返回工具边界：项目元数据位于隐藏工作区时返回 .pentgo 的父目录，
// 否则返回项目根目录本身。
func (store *ProjectStore) WorkspaceRoot() string {
	root := store.Root()
	if filepath.Base(root) == ".pentgo" {
		return filepath.Dir(root)
	}
	return root
}

// DatabasePath 返回此存储的 SQLite 文件位置。
func (store *ProjectStore) DatabasePath() string { return filepath.Join(store.Root(), "pentgo.db") }

// ClaimNotice atomically records a one-time project notice and reports whether
// this caller is the first to claim it.
func (store *ProjectStore) ClaimNotice(key string) (bool, error) {
	if store == nil || store.db == nil || strings.TrimSpace(key) == "" {
		return false, fmt.Errorf("project notice key is invalid")
	}
	result, err := store.db.Exec("INSERT INTO project_notices(notice_key) VALUES(?) ON CONFLICT(notice_key) DO NOTHING", key)
	if err != nil {
		return false, fmt.Errorf("claim project notice: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect project notice claim: %w", err)
	}
	return claimed != 0, nil
}

// TmpDir 返回此项目专用的 MCP 和临时进程存储目录。
func (store *ProjectStore) TmpDir() string { return filepath.Join(store.Root(), "tmp") }

// LoadProject 读取项目元数据，并从会话行推导会话摘要索引，
// 而不信任重复持久化的列表。
func (store *ProjectStore) LoadProject() (*Project, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project store is nil")
	}
	var project Project
	var createdAt, updatedAt int64
	if err := store.db.QueryRow("SELECT id, name, created_at, updated_at FROM projects WHERE singleton = 1").Scan(&project.ID, &project.Name, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	project.CreatedAt = parseTime(createdAt)
	project.UpdatedAt = parseTime(updatedAt)
	rows, err := store.db.Query("SELECT id, updated_at FROM sessions ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("load project sessions: %w", err)
	}
	defer rows.Close()
	project.Sessions = []SessionSummary{}
	for rows.Next() {
		var summary SessionSummary
		var value int64
		if err := rows.Scan(&summary.ID, &value); err != nil {
			return nil, fmt.Errorf("load project session: %w", err)
		}
		summary.UpdatedAt = parseTime(value)
		project.Sessions = append(project.Sessions, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load project sessions: %w", err)
	}
	return &project, nil
}

// LoadSession 重建一个会话、其目标和最新 turn。
func (store *ProjectStore) LoadSession(id string) (*sessionstate.Session, error) {
	if store == nil || store.db == nil {
		return nil, fmt.Errorf("project store is nil")
	}
	if !validID(id) {
		return nil, fmt.Errorf("invalid session id")
	}
	return loadSessionQuery(store.db, id)
}

// loadSessionQuery 可与数据库或事务查询器协作，使规范化会话图只有一套重建实现。
func loadSessionQuery(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}, id string) (*sessionstate.Session, error) {
	var session sessionstate.Session
	var startedAt, updatedAt int64
	var lastTurnID, activeTurnID sql.NullString
	err := queryer.QueryRow(`
		SELECT id, name, target, intent, turn_count, last_turn_id,
		       active_turn_id, final_summary, started_at, updated_at
		FROM sessions WHERE id = ?`, id).Scan(
		&session.ID, &session.Name, &session.Target, &session.Intent,
		&session.Turns, &lastTurnID, &activeTurnID, &session.FinalSummary,
		&startedAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load session %q: %w", id, err)
	}
	session.ActiveTurnID = activeTurnID.String
	session.StartedAt = parseTime(startedAt)
	session.UpdatedAt = parseTime(updatedAt)
	rows, err := queryer.Query("SELECT target FROM session_targets WHERE session_id = ? ORDER BY position", id)
	if err != nil {
		return nil, fmt.Errorf("load session %q targets: %w", id, err)
	}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			rows.Close()
			return nil, fmt.Errorf("load session %q target: %w", id, err)
		}
		session.Targets = append(session.Targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("load session %q targets: %w", id, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("load session %q targets: %w", id, err)
	}
	if lastTurnID.Valid {
		turn, err := loadTurn(queryer, lastTurnID.String)
		if err != nil {
			return nil, fmt.Errorf("load session %q turn: %w", id, err)
		}
		session.ActiveTurn = turn
	}
	return &session, nil
}

// loadTurn 恢复最后一次持久化的 turn，包括其可选结束时间。
func loadTurn(queryer interface{ QueryRow(string, ...any) *sql.Row }, id string) (*sessionstate.Turn, error) {
	var turn sessionstate.Turn
	var status string
	var startedAt int64
	var finishedAt sql.NullInt64
	if err := queryer.QueryRow(`
		SELECT id, session_id, message, status, started_at, finished_at, error
		FROM turns WHERE id = ?`, id).Scan(
		&turn.ID, &turn.SessionID, &turn.Message, &status, &startedAt, &finishedAt, &turn.Error,
	); err != nil {
		return nil, err
	}
	turn.Status = sessionstate.TurnStatus(status)
	turn.StartedAt = parseTime(startedAt)
	if finishedAt.Valid {
		value := parseTime(finishedAt.Int64)
		turn.FinishedAt = &value
	}
	return &turn, nil
}

// SaveSession 原子持久化一个会话及其规范化目标。
func (store *ProjectStore) SaveSession(session *sessionstate.Session) error {
	if store == nil || store.db == nil || session == nil || !validID(session.ID) {
		return fmt.Errorf("session is invalid")
	}
	store.commitMu.Lock()
	defer store.commitMu.Unlock()
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin session save: %w", err)
	}
	if err := saveSessionTx(tx, session); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session save: %w", err)
	}
	return nil
}

// CommitSession 在一个事务中持久化会话和项目元数据，
// 确保可重建的项目索引不会领先于会话状态。
func (store *ProjectStore) CommitSession(session *sessionstate.Session, project *Project) error {
	if store == nil || store.db == nil || session == nil || project == nil || !validID(session.ID) {
		return fmt.Errorf("session commit is invalid")
	}
	store.commitMu.Lock()
	defer store.commitMu.Unlock()
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin session commit: %w", err)
	}
	if err := saveSessionTx(tx, session); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := saveProjectTx(tx, project); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session: %w", err)
	}
	return nil
}

// saveSessionTx 更新或插入会话和最新 turn，再替换有序目标。
// 它始终在调用方持有的事务中执行。
func saveSessionTx(tx *sql.Tx, session *sessionstate.Session) error {
	name := strings.TrimSpace(session.Name)
	if name == "" {
		name = session.ID
	}
	startedAt := session.StartedAt
	if startedAt.IsZero() {
		startedAt = session.UpdatedAt
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	updatedAt := session.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = startedAt
	}
	lastTurnID := ""
	activeTurnID := strings.TrimSpace(session.ActiveTurnID)
	if session.ActiveTurn != nil {
		lastTurnID = session.ActiveTurn.ID
	}
	if _, err := tx.Exec(`
		INSERT INTO sessions(
			id, name, target, intent, turn_count, last_turn_id,
			active_turn_id, final_summary, started_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			target = excluded.target,
			intent = excluded.intent,
			turn_count = excluded.turn_count,
			last_turn_id = excluded.last_turn_id,
			active_turn_id = excluded.active_turn_id,
			final_summary = excluded.final_summary,
			started_at = excluded.started_at,
			updated_at = excluded.updated_at`,
		session.ID, name, session.Target, session.Intent, session.Turns,
		nullableText(lastTurnID), nullableText(activeTurnID), session.FinalSummary,
		timeValue(startedAt), timeValue(updatedAt),
	); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	if session.ActiveTurn != nil {
		if _, err := tx.Exec(`
			INSERT INTO turns(id, session_id, message, status, started_at, finished_at, error)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				session_id = excluded.session_id,
				message = excluded.message,
				status = excluded.status,
				started_at = excluded.started_at,
				finished_at = excluded.finished_at,
				error = excluded.error`,
			session.ActiveTurn.ID, session.ID, session.ActiveTurn.Message,
			session.ActiveTurn.Status, timeValue(session.ActiveTurn.StartedAt),
			nullableTime(session.ActiveTurn.FinishedAt), session.ActiveTurn.Error,
		); err != nil {
			return fmt.Errorf("save session turn: %w", err)
		}
	}
	if _, err := tx.Exec("DELETE FROM session_targets WHERE session_id = ?", session.ID); err != nil {
		return fmt.Errorf("replace session targets: %w", err)
	}
	seen := make(map[string]bool)
	targets := append([]string(nil), session.Targets...)
	if session.Target != "" {
		targets = append([]string{session.Target}, targets...)
	}
	position := 0
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		if _, err := tx.Exec("INSERT INTO session_targets(session_id, position, target) VALUES(?, ?, ?)", session.ID, position, target); err != nil {
			return fmt.Errorf("save session target: %w", err)
		}
		position++
	}
	return nil
}

// nullableTime 将缺失的 turn 完成时间转换为 SQL NULL。
func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timeValue(*value)
}

// DeleteSession 通过数据库外键删除会话图、更新项目元数据，
// 并清理废弃的旧版磁盘会话目录。
func (store *ProjectStore) DeleteSession(id string) error {
	if store == nil || store.db == nil || !validID(id) {
		return fmt.Errorf("session id is invalid")
	}
	store.commitMu.Lock()
	defer store.commitMu.Unlock()
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin session delete: %w", err)
	}
	result, err := tx.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	if deleted == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("session %q does not exist", id)
	}
	if _, err := tx.Exec("UPDATE projects SET updated_at = ? WHERE singleton = 1", timeValue(time.Now().UTC())); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update project after deleting session %q: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session delete %q: %w", id, err)
	}
	_ = os.RemoveAll(filepath.Join(store.root, "sessions", id))
	_ = os.RemoveAll(filepath.Join(store.root, "resume", id))
	return nil
}

// SaveProjectIndex 为管理调用方写入唯一项目元数据。
func (store *ProjectStore) SaveProjectIndex(project *Project) error {
	if store == nil || store.db == nil || project == nil || strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Name) == "" {
		return fmt.Errorf("project is invalid")
	}
	store.commitMu.Lock()
	defer store.commitMu.Unlock()
	return store.saveProject(project)
}

// saveProject 使用独立事务封装项目行持久化。
func (store *ProjectStore) saveProject(project *Project) error {
	tx, err := store.db.Begin()
	if err != nil {
		return fmt.Errorf("begin project save: %w", err)
	}
	if err := saveProjectTx(tx, project); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project save: %w", err)
	}
	return nil
}

// saveProjectTx 更新或插入唯一项目元数据行。
func saveProjectTx(tx *sql.Tx, project *Project) error {
	if project == nil || strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Name) == "" {
		return fmt.Errorf("project is invalid")
	}
	createdAt := project.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := project.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	if _, err := tx.Exec(`
		INSERT INTO projects(singleton, id, name, created_at, updated_at)
		VALUES(1, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			id = excluded.id,
			name = excluded.name,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at`,
		project.ID, strings.TrimSpace(project.Name), timeValue(createdAt), timeValue(updatedAt),
	); err != nil {
		return fmt.Errorf("save project: %w", err)
	}
	return nil
}

// Close 在并发提交调用方结束后仅关闭一次 SQLite 连接。
func (store *ProjectStore) Close() error {
	if store == nil {
		return nil
	}
	store.closeMu.Lock()
	defer store.closeMu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	if store.db == nil {
		return nil
	}
	return store.db.Close()
}

// validID 拒绝路径形式的值，因为 ID 也用于旧版清理逻辑。
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsRune(id, filepath.Separator)
}

// newID 分配不透明的存储 ID，不暴露 SQLite 行标识。
func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-unknown"
	}
	return prefix + "-" + hex.EncodeToString(value)
}
