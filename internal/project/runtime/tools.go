package runtime

import (
	"context"
	"fmt"
	"sync"

	"pentgo/internal/core"
	builtins "pentgo/internal/tools"
)

// combinedToolProvider joins built-in and external MCP providers before a
// session starts. runtimeToolProvider remains responsible for evidence wrapping.
type combinedToolProvider struct {
	providers []core.ToolProvider
	closers   []core.ToolCloser
	closeOnce sync.Once
	closeErr  error
}

func combineToolProviders(providers ...core.ToolProvider) *combinedToolProvider {
	combined := &combinedToolProvider{}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		combined.providers = append(combined.providers, provider)
		if closer, ok := provider.(core.ToolCloser); ok {
			combined.closers = append(combined.closers, closer)
		}
	}
	return combined
}

func (provider *combinedToolProvider) Tools(ctx context.Context) ([]core.Tool, error) {
	if provider == nil {
		return nil, nil
	}
	tools := make([]core.Tool, 0)
	seen := make(map[string]bool)
	for _, source := range provider.providers {
		discovered, err := source.Tools(ctx)
		if err != nil {
			return nil, err
		}
		for _, tool := range discovered {
			if tool == nil {
				return nil, fmt.Errorf("tool provider returned nil tool")
			}
			name := tool.Name()
			if seen[name] {
				return nil, fmt.Errorf("tool name collision: %s", name)
			}
			seen[name] = true
			tools = append(tools, tool)
		}
	}
	return append([]core.Tool(nil), tools...), nil
}

// validateProjectTools rejects names that would collide with tools added for
// every session. It runs before session restoration so a project never opens
// with a provider that is guaranteed to fail on its first turn.
func validateProjectTools(ctx context.Context, provider core.ToolProvider, skillsAvailable bool) error {
	if provider == nil {
		return nil
	}
	tools, err := provider.Tools(ctx)
	if err != nil {
		return err
	}
	reserved := map[string]bool{
		"upsert_project_fact": true,
		"get_project_fact":    true,
		"list_project_facts":  true,
	}
	if skillsAvailable {
		reserved["load_skill"] = true
	}
	for _, tool := range tools {
		if tool == nil {
			return fmt.Errorf("tool provider returned nil tool")
		}
		if builtins.IsName(tool.Name()) || reserved[tool.Name()] {
			return fmt.Errorf("tool name collision: %s", tool.Name())
		}
	}
	return nil
}

func (provider *combinedToolProvider) Close() error {
	if provider == nil {
		return nil
	}
	provider.closeOnce.Do(func() {
		for _, closer := range provider.closers {
			if err := closer.Close(); provider.closeErr == nil && err != nil {
				provider.closeErr = err
			}
		}
	})
	return provider.closeErr
}

var _ core.ToolProvider = (*combinedToolProvider)(nil)
var _ core.ToolCloser = (*combinedToolProvider)(nil)
