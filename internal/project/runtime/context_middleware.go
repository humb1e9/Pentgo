package runtime

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"pentgo/internal/core"
)

// ContextMiddlewareConfig contains the per-run context dependencies.
type ContextMiddlewareConfig struct {
	SessionID    string
	Window       *ContextWindow
	Conversation func() []core.Message
	ToolProvider core.ToolProvider
	BuildTools   func(context.Context) ([]core.Tool, error)
	Facts        func(context.Context) (string, error)
}

// ContextMiddleware is the sole owner of the model-visible message list.
type ContextMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	config ContextMiddlewareConfig
}

type runtimeToolsKey struct{}

func NewContextMiddleware(config ContextMiddlewareConfig) *ContextMiddleware {
	return &ContextMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, config: config}
}

func (middleware *ContextMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	if middleware == nil || middleware.config.Window == nil {
		return ctx, runCtx, fmt.Errorf("agent context window is not configured")
	}
	if runCtx == nil {
		return ctx, nil, fmt.Errorf("agent run context is nil")
	}
	tools, err := middleware.resolveTools(ctx)
	if err != nil {
		return ctx, runCtx, err
	}
	base := append([]tool.BaseTool(nil), runCtx.Tools...)
	for _, projectTool := range tools {
		if projectTool == nil {
			return ctx, runCtx, fmt.Errorf("agent context tool is nil")
		}
		adapter, err := newCoreToolAdapter(projectTool)
		if err != nil {
			return ctx, runCtx, err
		}
		base = append(base, adapter)
	}
	runCtx.Tools = base
	return context.WithValue(ctx, runtimeToolsKey{}, tools), runCtx, nil
}

func (middleware *ContextMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if middleware == nil || middleware.config.Window == nil {
		return ctx, state, fmt.Errorf("agent context window is not configured")
	}
	if state == nil {
		return ctx, nil, fmt.Errorf("agent state is nil")
	}
	facts, err := middleware.resolveFacts(ctx)
	if err != nil {
		return ctx, state, err
	}
	if middleware.config.Conversation == nil {
		return ctx, state, fmt.Errorf("agent conversation source is not configured")
	}
	messages, err := middleware.config.Window.Messages(ctx, middleware.config.SessionID, middleware.config.Conversation(), facts)
	if err != nil {
		return ctx, state, err
	}
	state.Messages = toEinoMessages(messages)
	return ctx, state, nil
}

func (middleware *ContextMiddleware) resolveTools(ctx context.Context) ([]core.Tool, error) {
	if middleware.config.BuildTools != nil {
		return middleware.config.BuildTools(ctx)
	}
	if middleware.config.ToolProvider != nil {
		return middleware.config.ToolProvider.Tools(ctx)
	}
	return nil, nil
}

func (middleware *ContextMiddleware) resolveFacts(ctx context.Context) (string, error) {
	if middleware.config.Facts != nil {
		return middleware.config.Facts(ctx)
	}
	return "", nil
}

var _ adk.ChatModelAgentMiddleware = (*ContextMiddleware)(nil)
