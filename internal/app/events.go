package app

// Event 是 SessionWorker 发出的、面向 UI 的可丢失进度通知。
// 对话的持久化状态始终以 transcript 为准。
type Event struct {
	SessionID string
	TurnID    string
	Kind      string
	Message   string
	Output    string
	Data      any
}

// 事件类型描述终端消费者需要展示的生命周期细节。
const (
	EventTurnStarted      = "turn_started"
	EventToolStarted      = "tool_started"
	EventToolFinished     = "tool_finished"
	EventAssistantMessage = "assistant_message"
	EventTurnFinished     = "turn_finished"
	EventTurnFailed       = "turn_failed"
)
