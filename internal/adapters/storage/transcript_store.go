package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"pentgo/internal/agent"
)

// TranscriptStore 以严格顺序追加一个会话的模型可见消息历史，
// 并缓存副本以降低后续 turn 回放开销。
type TranscriptStore struct {
	mu        sync.Mutex
	db        *sql.DB
	path      string
	sessionID string
	messages  []agent.Message
	failed    error
	closed    bool
}

// OpenTranscript 打开独立的 SQLite 连接，并加载一个有效会话的完整有序消息历史。
func (store *ProjectStore) OpenTranscript(id string) (*TranscriptStore, error) {
	if store == nil || store.db == nil || !validID(id) {
		return nil, fmt.Errorf("invalid transcript session id")
	}
	db, err := openSQLite(store.DatabasePath())
	if err != nil {
		return nil, err
	}
	messages, err := loadTranscriptDB(db, id)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &TranscriptStore{db: db, path: store.DatabasePath(), sessionID: id, messages: messages}, nil
}

// Path 返回包含 transcript 的 SQLite 数据库。
func (store *TranscriptStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Append 在加入内存回放缓存前提交消息。
// 数据库失败会成为持续状态，因为缓存序列将因此与持久化数据分叉。
func (store *TranscriptStore) Append(message agent.Message) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("transcript store is nil")
	}
	if strings.TrimSpace(message.Role) == "" {
		return fmt.Errorf("transcript message role is empty")
	}
	message = cloneMessage(message)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failed != nil {
		return store.failed
	}
	if store.closed {
		store.failed = fmt.Errorf("append transcript: closed")
		return store.failed
	}
	tx, err := store.db.Begin()
	if err != nil {
		store.failed = fmt.Errorf("append transcript: %w", err)
		return store.failed
	}
	if _, err := insertNextMessageTx(tx, store.sessionID, message); err != nil {
		_ = tx.Rollback()
		store.failed = fmt.Errorf("append transcript: %w", err)
		return store.failed
	}
	if err := tx.Commit(); err != nil {
		store.failed = fmt.Errorf("append transcript: %w", err)
		return store.failed
	}
	store.messages = append(store.messages, message)
	return nil
}

// AppendBatch commits an ordered group of messages as one transaction before
// publishing any of them to the in-memory replay cache. It is used for one
// assistant tool-call batch's results so recovery never observes only a prefix.
func (store *TranscriptStore) AppendBatch(messages []agent.Message) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("transcript store is nil")
	}
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]agent.Message, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("transcript message role is empty")
		}
		cloned[index] = cloneMessage(message)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failed != nil {
		return store.failed
	}
	if store.closed {
		store.failed = fmt.Errorf("append transcript batch: closed")
		return store.failed
	}
	tx, err := store.db.Begin()
	if err != nil {
		store.failed = fmt.Errorf("append transcript batch: %w", err)
		return store.failed
	}
	for _, message := range cloned {
		if _, err := insertNextMessageTx(tx, store.sessionID, message); err != nil {
			_ = tx.Rollback()
			store.failed = fmt.Errorf("append transcript batch: %w", err)
			return store.failed
		}
	}
	if err := tx.Commit(); err != nil {
		store.failed = fmt.Errorf("append transcript batch: %w", err)
		return store.failed
	}
	store.messages = append(store.messages, cloned...)
	return nil
}

// insertNextMessageTx 在与其工具调用相同的事务中分配 seq，
// 从而在回放时保留精确的模型消息顺序。
func insertNextMessageTx(tx *sql.Tx, sessionID string, message agent.Message) (int, error) {
	argumentsJSON, err := marshalNullableJSON(message.ToolArguments)
	if err != nil {
		return 0, fmt.Errorf("encode transcript tool arguments: %w", err)
	}
	var sequence int
	if err := tx.QueryRow(`
		INSERT INTO transcript_messages(
			session_id, seq, role, content, reasoning_content, tool_call_id, tool_name, tool_arguments_json
		)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?, ?
		FROM transcript_messages WHERE session_id = ?
		RETURNING seq`,
		sessionID, message.Role, message.Content, message.ReasoningContent, message.ToolCallID,
		message.ToolName, argumentsJSON, sessionID,
	).Scan(&sequence); err != nil {
		return 0, err
	}
	if err := insertToolCallsTx(tx, sessionID, sequence, message.ToolCalls); err != nil {
		return 0, err
	}
	return sequence, nil
}

// insertToolCallsTx 单独存储助手工具调用，以保留顺序和格式错误的原始参数，
// 无需将整条消息序列化为 JSON。
func insertToolCallsTx(tx *sql.Tx, sessionID string, sequence int, calls []agent.ToolCall) error {
	for position, call := range calls {
		callArguments, err := marshalNullableJSON(call.Arguments)
		if err != nil {
			return fmt.Errorf("encode transcript tool call arguments: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO transcript_tool_calls(
				session_id, message_seq, position, id, name, arguments_json, raw_arguments
			) VALUES(?, ?, ?, ?, ?, ?, ?)`,
			sessionID, sequence, position, call.ID, call.Name, callArguments, call.RawArguments,
		); err != nil {
			return err
		}
	}
	return nil
}

// marshalNullableJSON 将缺失的参数映射保留为 SQL NULL，而非 {}。
func marshalNullableJSON(value map[string]any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// Messages 返回深拷贝，避免模型适配器修改回放历史。
func (store *TranscriptStore) Messages() []agent.Message {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	messages := make([]agent.Message, len(store.messages))
	for index, message := range store.messages {
		messages[index] = cloneMessage(message)
	}
	return messages
}

// cloneMessage 复制一条消息中的嵌套工具调用和参数映射。
func cloneMessage(message agent.Message) agent.Message {
	cloned := message
	cloned.ToolArguments = cloneArguments(message.ToolArguments)
	if message.ToolCalls != nil {
		cloned.ToolCalls = make([]agent.ToolCall, len(message.ToolCalls))
		for index, call := range message.ToolCalls {
			cloned.ToolCalls[index] = call
			cloned.ToolCalls[index].Arguments = cloneArguments(call.Arguments)
		}
	}
	return cloned
}

// cloneArguments 递归复制 JSON 风格的工具参数值。
func cloneArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

// cloneValue 处理 JSON 解码产生的映射和切片形式。
func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneArguments(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}

// Close 释放此 transcript 独占的数据库连接。
func (store *TranscriptStore) Close() error {
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
		store.failed = fmt.Errorf("close transcript: %w", err)
	}
	return store.failed
}

// loadTranscriptDB 从规范化表中重建消息及其有序工具调用子项；
// 回放期间绝不调用历史工具。
func loadTranscriptDB(db *sql.DB, sessionID string) ([]agent.Message, error) {
	return loadTranscriptQueryer(db, sessionID)
}

type transcriptQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

// loadTranscriptQueryer reconstructs messages through either a database handle
// or the transaction that captured the paired Context Surface snapshot.
func loadTranscriptQueryer(db transcriptQueryer, sessionID string) ([]agent.Message, error) {
	rows, err := db.Query(`
		SELECT seq, role, content, reasoning_content, tool_call_id, tool_name, tool_arguments_json
		FROM transcript_messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	messages := make([]agent.Message, 0)
	messageIndexes := make(map[int]int)
	for rows.Next() {
		var sequence int
		var message agent.Message
		var argumentsJSON sql.NullString
		if err := rows.Scan(&sequence, &message.Role, &message.Content, &message.ReasoningContent, &message.ToolCallID, &message.ToolName, &argumentsJSON); err != nil {
			return nil, fmt.Errorf("read transcript message: %w", err)
		}
		if argumentsJSON.Valid {
			if err := json.Unmarshal([]byte(argumentsJSON.String), &message.ToolArguments); err != nil {
				return nil, fmt.Errorf("decode transcript tool arguments: %w", err)
			}
		}
		messageIndexes[sequence] = len(messages)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	rows, err = db.Query(`
		SELECT message_seq, id, name, arguments_json, raw_arguments
		FROM transcript_tool_calls
		WHERE session_id = ? ORDER BY message_seq, position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read transcript tool calls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var call agent.ToolCall
		var argumentsJSON sql.NullString
		if err := rows.Scan(&sequence, &call.ID, &call.Name, &argumentsJSON, &call.RawArguments); err != nil {
			return nil, fmt.Errorf("read transcript tool call: %w", err)
		}
		if argumentsJSON.Valid {
			if err := json.Unmarshal([]byte(argumentsJSON.String), &call.Arguments); err != nil {
				return nil, fmt.Errorf("decode transcript tool call arguments: %w", err)
			}
		}
		index, exists := messageIndexes[sequence]
		if !exists {
			return nil, fmt.Errorf("read transcript tool call: message %d does not exist", sequence)
		}
		messages[index].ToolCalls = append(messages[index].ToolCalls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read transcript tool calls: %w", err)
	}
	return messages, nil
}
