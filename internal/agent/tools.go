package agent

import (
	"context"
	"fmt"
	"pentgo/internal/tools"
	"sync"
)

// combinedToolProvider joins built-in and external MCP providers before a
// session starts. runtimeToolProvider remains responsible for evidence wrapping.
type combinedToolProvider struct {
	providers []tools.Provider
	closers   []tools.Closer
	closeOnce sync.Once
	closeErr  error
}

func combineToolProviders(providers ...tools.Provider) *combinedToolProvider {
	combined := &combinedToolProvider{}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		combined.providers = append(combined.providers, provider)
		if closer, ok := provider.(tools.Closer); ok {
			combined.closers = append(combined.closers, closer)
		}
	}
	return combined
}

func (provider *combinedToolProvider) Tools(ctx context.Context) ([]tools.Tool, error) {
	if provider == nil {
		return nil, nil
	}
	discoveredTools := make([]tools.Tool, 0)
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
			discoveredTools = append(discoveredTools, tool)
		}
	}
	return append([]tools.Tool(nil), discoveredTools...), nil
}

// validateProjectTools rejects names that would collide with tools added for
// every session. It runs before session restoration so a project never opens
// with a provider that is guaranteed to fail on its first turn.
func validateProjectTools(ctx context.Context, provider tools.Provider) error {
	if provider == nil {
		return nil
	}
	discovered, err := provider.Tools(ctx)
	if err != nil {
		return err
	}
	reserved := map[string]bool{
		tools.FactUpsertName: true,
		tools.FactGetName:    true,
		tools.FactListName:   true,
	}
	for _, tool := range discovered {
		if tool == nil {
			return fmt.Errorf("tool provider returned nil tool")
		}
		if tools.IsName(tool.Name()) || reserved[tool.Name()] {
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

var _ tools.Provider = (*combinedToolProvider)(nil)
var _ tools.Closer = (*combinedToolProvider)(nil)
