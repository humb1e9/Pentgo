package agent

import (
	contextpolicy "pentgo/internal/context"
	"pentgo/internal/core"
	"pentgo/internal/model"
	"pentgo/internal/project"
)

// ContextWindow is the context policy used by the ADK middleware.
type ContextWindow = contextpolicy.ContextWindow

// SummaryInput is the text-only input for rolling summaries.
type SummaryInput = contextpolicy.SummaryInput

// SummaryWriter produces rolling summaries.
type SummaryWriter = contextpolicy.SummaryWriter

// SummaryModelFactory creates a model for summary generation.
type SummaryModelFactory = contextpolicy.SummaryModelFactory

func NewContextWindow(store contextpolicy.SummaryStore, cfg project.ContextConfig, summarizer SummaryWriter, fixedTokens int) *ContextWindow {
	return contextpolicy.NewContextWindow(store, cfg, summarizer, fixedTokens)
}

func NewModelSummaryWriter(factory SummaryModelFactory, configuration model.Config) SummaryWriter {
	return contextpolicy.NewModelSummaryWriter(factory, configuration)
}

func estimateTextTokens(value string) int { return contextpolicy.EstimateTextTokens(value) }
func estimateMessageTokens(messages []core.Message) int {
	return contextpolicy.EstimateMessageTokens(messages)
}
func estimateToolTokens(tools []core.Tool) int { return contextpolicy.EstimateToolTokens(tools) }
