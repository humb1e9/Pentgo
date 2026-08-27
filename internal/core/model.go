package core

import (
	"context"
	"errors"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type ToolCall struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RawArguments string         `json:"raw_arguments,omitempty"`
}

type Message struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	ToolArguments    map[string]any `json:"tool_arguments,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
}

type ModelStepInput struct {
	SessionID       string
	Messages        []Message
	SystemPrompt    string
	ProjectFacts    string
	Tools           []Tool
	MaxOutputTokens int
}

type ModelStreamEvent struct {
	Delta        Message
	Final        *Message
	FinishReason string
	Err          error
}

var ErrContextWindowExceeded = errors.New("model context window exceeded")

type ModelStepper interface {
	StreamStep(context.Context, ModelStepInput) (<-chan ModelStreamEvent, error)
}
