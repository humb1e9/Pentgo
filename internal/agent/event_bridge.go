package agent

import (
	"context"
	"fmt"
	"io"
	"pentgo/internal/session"

	sessionstate "pentgo/internal/session"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// EventBridge persists the durable parts of an agent event stream and
// forwards its displayable progress to the existing turn event sink.
type EventBridge struct {
	conversation *sessionstate.ConversationStore
	sessionID    string
	turnID       string
	emit         func(sessionstate.Event)

	persistedCalls   map[string]struct{}
	persistedResults map[string]struct{}
	pendingCalls     []session.ToolCall
	pendingResults   map[string]session.Message
}

// NewEventBridge creates a bridge for one running turn. The caller remains
// responsible for final session-state persistence and turn completion.
func NewEventBridge(conversation *sessionstate.ConversationStore, sessionID, turnID string, emit func(sessionstate.Event)) (*EventBridge, error) {
	if conversation == nil {
		return nil, fmt.Errorf("agent event bridge conversation is nil")
	}
	persistedCalls := make(map[string]struct{})
	persistedResults := make(map[string]struct{})
	for _, message := range conversation.Messages() {
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				persistedCalls[call.ID] = struct{}{}
			}
		}
		if message.Role == session.RoleTool && message.ToolCallID != "" {
			persistedResults[message.ToolCallID] = struct{}{}
		}
	}
	return &EventBridge{
		conversation:     conversation,
		sessionID:        sessionID,
		turnID:           turnID,
		emit:             emit,
		persistedCalls:   persistedCalls,
		persistedResults: persistedResults,
		pendingResults:   make(map[string]session.Message),
	}, nil
}

// Consume drains an Eino v0.9.13 AsyncIterator and persists the message
// sequence visible to the next agent turn. Tool results are appended only as a
// complete, call-order-preserving batch after the middleware has produced them.
func (bridge *EventBridge) Consume(ctx context.Context, events *adk.AsyncIterator[*adk.AgentEvent]) error {
	if bridge == nil {
		return fmt.Errorf("agent event bridge is nil")
	}
	if events == nil {
		return fmt.Errorf("agent event iterator is nil")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		event, ok := events.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return event.Err
		}
		message, role, err := bridge.message(event)
		if err != nil {
			return err
		}
		if message == nil {
			continue
		}
		switch role {
		case schema.Assistant:
			if err := bridge.assistant(fromEinoMessage(message)); err != nil {
				return err
			}
		case schema.Tool:
			if err := bridge.tool(fromEinoMessage(message)); err != nil {
				return err
			}
		}
	}
	if len(bridge.pendingCalls) != 0 {
		return fmt.Errorf("agent event stream ended before tool result batch completed")
	}
	return nil
}

func (bridge *EventBridge) message(event *adk.AgentEvent) (*schema.Message, schema.RoleType, error) {
	if event.Output == nil || event.Output.MessageOutput == nil {
		return nil, "", nil
	}
	output := event.Output.MessageOutput
	role := output.Role
	if !output.IsStreaming {
		if output.Message == nil {
			return nil, role, nil
		}
		if role == "" {
			role = output.Message.Role
		}
		return output.Message, role, nil
	}
	if output.MessageStream == nil {
		return nil, role, fmt.Errorf("agent event has nil message stream")
	}
	defer output.MessageStream.Close()
	var chunks []*schema.Message
	for {
		chunk, err := output.MessageStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, role, fmt.Errorf("read agent message stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		if role == "" {
			role = chunk.Role
		}
		if role == schema.Assistant && chunk.Content != "" {
			bridge.emitEvent(sessionstate.Event{Kind: sessionstate.EventAssistantDelta, Message: chunk.Content})
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, role, nil
	}
	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, role, fmt.Errorf("concat agent message stream: %w", err)
	}
	if role == "" {
		role = message.Role
	}
	return message, role, nil
}

func (bridge *EventBridge) assistant(message session.Message) error {
	if message.Role == "" {
		message.Role = session.RoleAssistant
	}
	if len(message.ToolCalls) == 0 {
		if err := bridge.flushResults(); err != nil {
			return err
		}
		if err := bridge.conversation.Append(message); err != nil {
			return err
		}
		bridge.emitEvent(sessionstate.Event{Kind: sessionstate.EventAssistantMessage, Message: message.Content})
		return nil
	}
	filtered := message
	filtered.ToolCalls = filtered.ToolCalls[:0]
	for _, call := range message.ToolCalls {
		if call.ID == "" {
			return fmt.Errorf("tool call ID is empty")
		}
		if _, exists := bridge.persistedCalls[call.ID]; exists {
			continue
		}
		bridge.persistedCalls[call.ID] = struct{}{}
		filtered.ToolCalls = append(filtered.ToolCalls, call)
	}
	if len(filtered.ToolCalls) == 0 {
		return nil
	}
	if err := bridge.conversation.Append(filtered); err != nil {
		return err
	}
	bridge.pendingCalls = append(bridge.pendingCalls, filtered.ToolCalls...)
	for _, call := range filtered.ToolCalls {
		bridge.emitEvent(sessionstate.Event{Kind: sessionstate.EventToolStarted, Message: call.Name})
	}
	return nil
}

func (bridge *EventBridge) tool(message session.Message) error {
	if message.ToolCallID == "" {
		return fmt.Errorf("tool result ID is empty")
	}
	if _, exists := bridge.persistedResults[message.ToolCallID]; exists {
		return nil
	}
	if !bridge.awaiting(message.ToolCallID) {
		return fmt.Errorf("tool result %q has no pending tool call", message.ToolCallID)
	}
	message.Role = session.RoleTool
	bridge.persistedResults[message.ToolCallID] = struct{}{}
	bridge.pendingResults[message.ToolCallID] = message
	return bridge.flushResults()
}

func (bridge *EventBridge) awaiting(id string) bool {
	for _, call := range bridge.pendingCalls {
		if call.ID == id {
			return true
		}
	}
	return false
}

func (bridge *EventBridge) flushResults() error {
	if len(bridge.pendingCalls) == 0 || len(bridge.pendingResults) != len(bridge.pendingCalls) {
		return nil
	}
	messages := make([]session.Message, 0, len(bridge.pendingCalls))
	for _, call := range bridge.pendingCalls {
		message := bridge.pendingResults[call.ID]
		if message.ToolName == "" {
			message.ToolName = call.Name
		}
		messages = append(messages, message)
	}
	if err := bridge.conversation.AppendBatch(messages); err != nil {
		return err
	}
	for _, message := range messages {
		bridge.emitEvent(sessionstate.Event{Kind: sessionstate.EventToolFinished, Message: message.ToolName})
	}
	bridge.pendingCalls = nil
	bridge.pendingResults = make(map[string]session.Message)
	return nil
}

func (bridge *EventBridge) emitEvent(event sessionstate.Event) {
	if bridge.emit == nil {
		return
	}
	event.SessionID = bridge.sessionID
	event.TurnID = bridge.turnID
	bridge.emit(event)
}
