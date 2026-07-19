package loop

import (
	"unicode/utf8"

	"pentgo/internal/agent"
)

const (
	maxHistoryMessages = 16
	maxHistoryBytes    = 3000
)

// History 保存固定任务上下文及有界的模型对话记录。
type History struct {
	mission  agent.Message
	messages []agent.Message
}

// NewHistory 创建一份以目标和任务意图为固定首消息的历史。
func NewHistory(target, intent string) *History {
	return &History{mission: agent.Message{
		Role:    "user",
		Content: "TARGET: " + target + "\nTASK: " + intent,
	}}
}

// Append 追加一条对话消息，并裁剪为最多 16 条后续消息。
func (history *History) Append(role, content string) {
	if history == nil || content == "" {
		return
	}
	message := agent.Message{Role: role, Content: truncateBytes(content, maxHistoryBytes)}
	history.messages = append(history.messages, message)
	if len(history.messages) > maxHistoryMessages {
		history.messages = history.messages[len(history.messages)-maxHistoryMessages:]
	}
}

// Messages 返回包含固定任务上下文的副本，供模型请求使用。
func (history *History) Messages() []agent.Message {
	if history == nil {
		return nil
	}
	messages := make([]agent.Message, 0, len(history.messages)+1)
	messages = append(messages, history.mission)
	messages = append(messages, history.messages...)
	return messages
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	truncated := value[:limit]
	for !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
