package session

// Event is a lossy, discardable progress notification emitted by a worker.
// The durable dialogue state always lives in the conversation.
type Event struct {
	SessionID string
	TurnID    string
	Kind      string
	Message   string
}

const (
	EventTurnStarted      = "turn_started"
	EventToolStarted      = "tool_started"
	EventToolFinished     = "tool_finished"
	EventAssistantMessage = "assistant_message"
	EventAssistantDelta   = "assistant_delta"
	EventTurnFinished     = "turn_finished"
	EventTurnFailed       = "turn_failed"
)
