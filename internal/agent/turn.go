package agent

import "context"

// 应用层和模型适配层共用的消息角色与事件类型。
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	TurnEventMessage = "message"
	TurnEventError   = "error"
	TurnEventDone    = "done"
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
	Role          string         `json:"role"`
	Content       string         `json:"content,omitempty"`
	ToolCallID    string         `json:"tool_call_id,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	ToolArguments map[string]any `json:"tool_arguments,omitempty"`
	ToolCalls     []ToolCall     `json:"tool_calls,omitempty"`
}

// TurnInput 是单次模型执行的不可变输入。Messages 来自持久化 transcript 回放；
// 工具和提示词补充信息仅在本次执行中有效。
type TurnInput struct {
	SessionID     string
	Messages      []Message
	Tools         []Tool
	SystemPrompt  string
	ProjectFacts  string
	SkillSummary  string
	MaxIterations int
}

// TurnEvent 将模型输出流式传递给应用层。Message 事件保留 transcript 顺序，
// Error 和 Done 事件标记一次执行的终止状态。
type TurnEvent struct {
	Kind    string
	Message Message
	Tool    string
	Output  string
	Err     error
}

// ModelEngine 执行一轮模型调用并产生 Provider 无关事件。它不得持有领域状态，
// 因为 transcript 才是恢复会话的事实来源。
type ModelEngine interface {
	Run(context.Context, TurnInput) (<-chan TurnEvent, error)
}
