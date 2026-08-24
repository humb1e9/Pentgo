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

type SurfaceNodeKind string

const (
	SurfaceNodeSource     SurfaceNodeKind = "source"
	SurfaceNodeCheckpoint SurfaceNodeKind = "checkpoint"
	SurfaceNodePrunedTool SurfaceNodeKind = "pruned_tool"
)

type SurfaceNode struct {
	ID             string
	Position       int
	Kind           SurfaceNodeKind
	SourceStartSeq int
	SourceEndSeq   int
	Content        string
	Generation     int64
}

type ContextSurface struct {
	SessionID  string
	Generation int64
	Nodes      []SurfaceNode
}

type ContextMeasurement struct {
	SystemTokens     int
	ToolSchemaTokens int
	SurfaceTokens    int
	TotalTokens      int
}

type CheckpointInput struct {
	SystemPrompt    string
	Tools           []Tool
	Nodes           []SurfaceNode
	Messages        map[int]Message
	PriorCheckpoint string
	OutputTokenCap  int
	Prompt          string
}

type CheckpointOutput struct {
	Text      string
	Truncated bool
}

type ContextActivity struct {
	Kind    string
	Message string
}

const (
	ContextToolPruned        = "context_tool_pruned"
	ContextCheckpointCreated = "context_checkpoint_created"
	ContextRequestRejected   = "context_request_rejected"
	ContextOverflowRetry     = "context_overflow_retry"
)

type CompactionLifecycle struct {
	ID         string
	SessionID  string
	Generation int64
	StartSeq   int
	EndSeq     int
	Status     string
	Error      string
}

const (
	CompactionStarted   = "started"
	CompactionCommitted = "committed"
	CompactionFailed    = "failed"
)

type ModelStepInput struct {
	SessionID       string
	Messages        []Message
	SystemPrompt    string
	ProjectFacts    string
	Tools           []Tool
	ContextWindow   int
	MaxOutputTokens int
	SurfaceNodes    []SurfaceNode
	SurfaceMessages map[int]Message
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
