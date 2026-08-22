package agent

import (
	"context"
	"errors"
)

// 应用层和模型适配层共用的消息角色。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall 是单次助手工具调用的 Provider 无关表示。
// 当 Provider 返回格式错误的 JSON 时保留 RawArguments，以确保 transcript
// 如实记录 Provider 请求的内容。
type ToolCall struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RawArguments string         `json:"raw_arguments,omitempty"`
}

// Message 是应用层和模型适配层共用的持久化消息格式。ToolCalls 使用切片，
// 因为 Provider 可能在一条助手消息中请求多个工具。
type Message struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	ToolArguments    map[string]any `json:"tool_arguments,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
}

// ModelStepInput is one fully assembled, provider-visible request. Context
// management materializes persistent Surface nodes into Messages before this
// boundary; adapters must not read transcript or project state themselves.
type ModelStepInput struct {
	SessionID       string
	Messages        []Message
	SystemPrompt    string
	ProjectFacts    string
	Tools           []Tool
	ContextWindow   int
	SurfaceNodes    []SurfaceNode
	SurfaceMessages map[int]Message
}

// ModelStreamEvent is one event from a single provider request. Delta events
// are UI-only partial output. Exactly one event has Final set after the adapter
// has assembled every provider chunk; callers must not persist or execute a
// partial Delta. Err transports asynchronous stream failures.
type ModelStreamEvent struct {
	Delta Message
	Final *Message
	Err   error
}

// ErrContextWindowExceeded identifies a provider-confirmed context-window
// overflow. The host may compact the Context Surface and retry the pending
// provider request once; unknown provider errors must not be normalized to it.
var ErrContextWindowExceeded = errors.New("model context window exceeded")

// ModelStepper performs exactly one streamed provider request for an already
// assembled ModelStepInput. It binds the supplied tool schemas but never
// invokes tools; tool execution belongs to the host-controlled loop.
type ModelStepper interface {
	StreamStep(context.Context, ModelStepInput) (<-chan ModelStreamEvent, error)
}
