package agent

import (
	"context"
	"fmt"
	contextpolicy "pentgo/internal/context"
	"pentgo/internal/session"
	"pentgo/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

// ContextMiddlewareConfig contains the per-run context dependencies.
type ContextMiddlewareConfig struct {
	SessionID    string
	Window       *contextpolicy.ContextWindow
	Conversation func() []session.Message
	Tools        []tools.Tool
	Facts        func(context.Context) (string, error)
}

// ContextMiddleware is the sole owner of the model-visible message list.
type ContextMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	config ContextMiddlewareConfig
}

func (middleware *ContextMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	if middleware == nil || middleware.config.Window == nil {
		return ctx, runCtx, fmt.Errorf("agent context window is not configured")
	}
	if runCtx == nil {
		return ctx, nil, fmt.Errorf("agent run context is nil")
	}
	base := append([]tool.BaseTool(nil), runCtx.Tools...)
	for _, projectTool := range middleware.config.Tools {
		if projectTool == nil {
			return ctx, runCtx, fmt.Errorf("agent context tool is nil")
		}
		adapter, err := newEinoToolAdapter(projectTool)
		if err != nil {
			return ctx, runCtx, err
		}
		base = append(base, adapter)
	}
	runCtx.Tools = base
	return ctx, runCtx, nil
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

func (middleware *ContextMiddleware) resolveFacts(ctx context.Context) (string, error) {
	if middleware.config.Facts != nil {
		return middleware.config.Facts(ctx)
	}
	return "", nil
}

var _ adk.ChatModelAgentMiddleware = (*ContextMiddleware)(nil)
