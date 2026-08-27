package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"pentgo/internal/core"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einjsonschema "github.com/eino-contrib/jsonschema"
)

type coreToolAdapter struct {
	tool core.Tool
}

func newCoreToolAdapter(inner core.Tool) (tool.InvokableTool, error) {
	if inner == nil {
		return nil, fmt.Errorf("tool is nil")
	}
	return &coreToolAdapter{tool: inner}, nil
}

func (adapter *coreToolAdapter) Info(context.Context) (*schema.ToolInfo, error) {
	inputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	if provider, ok := adapter.tool.(core.ToolSchemaProvider); ok {
		if provided := provider.InputSchema(); provided != nil {
			inputSchema = provided
		}
	}
	data, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode tool %s schema: %w", adapter.tool.Name(), err)
	}
	var converted einjsonschema.Schema
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, fmt.Errorf("decode tool %s schema: %w", adapter.tool.Name(), err)
	}
	return &schema.ToolInfo{
		Name:        adapter.tool.Name(),
		Desc:        adapter.tool.Description(),
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&converted),
	}, nil
}

func (adapter *coreToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var arguments map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &arguments); err != nil {
		return "", fmt.Errorf("decode tool arguments: %w", err)
	}
	if arguments == nil {
		return "", fmt.Errorf("tool arguments must be a JSON object")
	}
	return adapter.tool.Invoke(ctx, arguments)
}

var _ tool.InvokableTool = (*coreToolAdapter)(nil)
