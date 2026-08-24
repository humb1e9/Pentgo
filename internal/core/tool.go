package core

import "context"

// Tool is a model-callable capability that observes context cancellation.
type Tool interface {
	Name() string
	Description() string
	Invoke(context.Context, map[string]any) (string, error)
}

// ToolSchemaProvider optionally supplies an explicit input schema.
type ToolSchemaProvider interface {
	InputSchema() map[string]any
}

// ToolProvider resolves tools available for one runtime context.
type ToolProvider interface {
	Tools(context.Context) ([]Tool, error)
}

// ToolCloser releases resources held by a tool provider.
type ToolCloser interface {
	Close() error
}
