package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"pentgo/internal/core"
)

// ConversationStore 以严格顺序追加一个会话的模型可见消息历史，
// 并缓存副本以降低后续 turn 回放开销。
type ConversationStore struct {
	mu        sync.Mutex
	db        *sql.DB
	path      string
	sessionID string
	messages  []core.Message
	failed    error
	closed    bool
}

// NewConversationStore constructs a conversation store bound to an existing
// database connection. The root package uses it to open a session conversation.
func NewConversationStore(db *sql.DB, path, sessionID string, messages []core.Message) *ConversationStore {
	return &ConversationStore{db: db, path: path, sessionID: sessionID, messages: messages}
}

// Path 返回包含 conversation 的 SQLite 数据库。
func (store *ConversationStore) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Append 在加入内存回放缓存前提交消息。
// 数据库失败会成为持续状态，因为缓存序列将因此与持久化数据分叉。
func (store *ConversationStore) Append(message core.Message) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("conversation store is nil")
	}
	if strings.TrimSpace(message.Role) == "" {
		return fmt.Errorf("conversation message role is empty")
	}
	message = core.CloneMessage(message)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failed != nil {
		return store.failed
	}
	if store.closed {
		store.failed = fmt.Errorf("append conversation: closed")
		return store.failed
	}
	tx, err := store.db.Begin()
	if err != nil {
		store.failed = fmt.Errorf("append conversation: %w", err)
		return store.failed
	}
	if _, err := insertNextMessageTx(tx, store.sessionID, message); err != nil {
		_ = tx.Rollback()
		store.failed = fmt.Errorf("append conversation: %w", err)
		return store.failed
	}
	if err := tx.Commit(); err != nil {
		store.failed = fmt.Errorf("append conversation: %w", err)
		return store.failed
	}
	store.messages = append(store.messages, message)
	return nil
}

// AppendBatch commits an ordered group of messages as one transaction before
// publishing any of them to the in-memory replay cache. It is used for one
// assistant tool-call batch's results so recovery never observes only a prefix.
func (store *ConversationStore) AppendBatch(messages []core.Message) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("conversation store is nil")
	}
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]core.Message, len(messages))
	for index, message := range messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("conversation message role is empty")
		}
		cloned[index] = core.CloneMessage(message)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failed != nil {
		return store.failed
	}
	if store.closed {
		store.failed = fmt.Errorf("append conversation batch: closed")
		return store.failed
	}
	tx, err := store.db.Begin()
	if err != nil {
		store.failed = fmt.Errorf("append conversation batch: %w", err)
		return store.failed
	}
	for _, message := range cloned {
		if _, err := insertNextMessageTx(tx, store.sessionID, message); err != nil {
			_ = tx.Rollback()
			store.failed = fmt.Errorf("append conversation batch: %w", err)
			return store.failed
		}
	}
	if err := tx.Commit(); err != nil {
		store.failed = fmt.Errorf("append conversation batch: %w", err)
		return store.failed
	}
	store.messages = append(store.messages, cloned...)
	return nil
}

// insertNextMessageTx 在与其工具调用相同的事务中分配 seq，
// 从而在回放时保留精确的模型消息顺序。
func insertNextMessageTx(tx *sql.Tx, sessionID string, message core.Message) (int, error) {
	argumentsJSON, err := marshalNullableJSON(message.ToolArguments)
	if err != nil {
		return 0, fmt.Errorf("encode conversation tool arguments: %w", err)
	}
	var sequence int
	if err := tx.QueryRow(`
		INSERT INTO conversation_messages(
			session_id, seq, role, content, reasoning_content, tool_call_id, tool_name, tool_arguments_json
		)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?, ?
		FROM conversation_messages WHERE session_id = ?
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
func insertToolCallsTx(tx *sql.Tx, sessionID string, sequence int, calls []core.ToolCall) error {
	for position, call := range calls {
		callArguments, err := marshalNullableJSON(call.Arguments)
		if err != nil {
			return fmt.Errorf("encode conversation tool call arguments: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO conversation_tool_calls(
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
func (store *ConversationStore) Messages() []core.Message {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	messages := make([]core.Message, len(store.messages))
	for index, message := range store.messages {
		messages[index] = core.CloneMessage(message)
	}
	return messages
}

// Close 释放此 conversation 独占的数据库连接。
func (store *ConversationStore) Close() error {
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
		store.failed = fmt.Errorf("close conversation: %w", err)
	}
	return store.failed
}

// loadConversationDB 从规范化表中重建消息及其有序工具调用子项；
// 回放期间绝不调用历史工具。
// LoadConversation rebuilds the ordered message history for one session from
// the normalized tables. The root package uses it when opening a session.
func LoadConversation(db *sql.DB, sessionID string) ([]core.Message, error) {
	return loadConversationQueryer(db, sessionID)
}

func loadConversationDB(db *sql.DB, sessionID string) ([]core.Message, error) {
	return loadConversationQueryer(db, sessionID)
}

// LoadConversationFrom reconstructs messages through any queryer, including
// the transaction that captured a paired context-surface snapshot.
func LoadConversationFrom(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, sessionID string) ([]core.Message, error) {
	return loadConversationQueryer(queryer, sessionID)
}

type conversationQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

// loadConversationQueryer reconstructs messages through either a database handle
// or the transaction that captured the paired Context Surface snapshot.
func loadConversationQueryer(db conversationQueryer, sessionID string) ([]core.Message, error) {
	rows, err := db.Query(`
		SELECT seq, role, content, reasoning_content, tool_call_id, tool_name, tool_arguments_json
		FROM conversation_messages WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read conversation: %w", err)
	}
	messages := make([]core.Message, 0)
	messageIndexes := make(map[int]int)
	for rows.Next() {
		var sequence int
		var message core.Message
		var argumentsJSON sql.NullString
		if err := rows.Scan(&sequence, &message.Role, &message.Content, &message.ReasoningContent, &message.ToolCallID, &message.ToolName, &argumentsJSON); err != nil {
			return nil, fmt.Errorf("read conversation message: %w", err)
		}
		if argumentsJSON.Valid {
			if err := json.Unmarshal([]byte(argumentsJSON.String), &message.ToolArguments); err != nil {
				return nil, fmt.Errorf("decode conversation tool arguments: %w", err)
			}
		}
		messageIndexes[sequence] = len(messages)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read conversation: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read conversation: %w", err)
	}
	rows, err = db.Query(`
		SELECT message_seq, id, name, arguments_json, raw_arguments
		FROM conversation_tool_calls
		WHERE session_id = ? ORDER BY message_seq, position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read conversation tool calls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var call core.ToolCall
		var argumentsJSON sql.NullString
		if err := rows.Scan(&sequence, &call.ID, &call.Name, &argumentsJSON, &call.RawArguments); err != nil {
			return nil, fmt.Errorf("read conversation tool call: %w", err)
		}
		if argumentsJSON.Valid {
			if err := json.Unmarshal([]byte(argumentsJSON.String), &call.Arguments); err != nil {
				return nil, fmt.Errorf("decode conversation tool call arguments: %w", err)
			}
		}
		index, exists := messageIndexes[sequence]
		if !exists {
			return nil, fmt.Errorf("read conversation tool call: message %d does not exist", sequence)
		}
		messages[index].ToolCalls = append(messages[index].ToolCalls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read conversation tool calls: %w", err)
	}
	return messages, nil
}
