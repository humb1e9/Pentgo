package runtime

import (
	contextpolicy "pentgo/internal/context"
	"pentgo/internal/core"
	"pentgo/internal/model"
	"pentgo/internal/project"
)

// Deprecated: use internal/context directly.
type ContextWindow = contextpolicy.ContextWindow

// Deprecated: use internal/context directly.
type SummaryInput = contextpolicy.SummaryInput

// Deprecated: use internal/context directly.
type SummaryWriter = contextpolicy.SummaryWriter

// Deprecated: use internal/context directly.
type SummaryModelFactory = contextpolicy.SummaryModelFactory

// Deprecated: use context.ContextWindow directly.
func NewContextWindow(store contextpolicy.SummaryStore, cfg project.ContextConfig, summarizer SummaryWriter, fixedTokens int) *ContextWindow {
	return contextpolicy.NewContextWindow(store, cfg, summarizer, fixedTokens)
}

// Deprecated: use context.NewModelSummaryWriter directly.
func NewModelSummaryWriter(factory SummaryModelFactory, configuration model.Config) SummaryWriter {
	return contextpolicy.NewModelSummaryWriter(factory, configuration)
}

// Deprecated: use context.EstimateTextTokens directly.
func estimateTextTokens(value string) int { return contextpolicy.EstimateTextTokens(value) }

// Deprecated: use context.EstimateMessageTokens directly.
func estimateMessageTokens(messages []core.Message) int {
	return contextpolicy.EstimateMessageTokens(messages)
}

// Deprecated: use context.EstimateToolTokens directly.
func estimateToolTokens(tools []core.Tool) int { return contextpolicy.EstimateToolTokens(tools) }
