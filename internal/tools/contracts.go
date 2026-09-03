package tools

import "context"

// Tool is a model-callable capability that observes context cancellation.
type Tool interface {
	Name() string
	Description() string
	// InputSchema supplies the JSON Schema for Invoke arguments.
	InputSchema() map[string]any
	Invoke(context.Context, map[string]any) (string, error)
}

// Provider resolves tools available for one runtime context.
type Provider interface {
	Tools(context.Context) ([]Tool, error)
}

// Closer releases resources held by a tool provider.
type Closer interface {
	Close() error
}
