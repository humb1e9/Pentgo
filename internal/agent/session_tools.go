package agent

import (
	"context"
	"fmt"
	"pentgo/internal/tools"

	sessionstate "pentgo/internal/session"
)

// runtimeToolProvider builds tools visible to one session turn.
type runtimeToolProvider struct {
	runtime      *ProjectRuntime
	session      *sessionstate.Session
	projectTools []tools.Tool
}

func newRuntimeToolProvider(runtime *ProjectRuntime, session *sessionstate.Session, projectTools []tools.Tool) *runtimeToolProvider {
	return &runtimeToolProvider{runtime: runtime, session: session, projectTools: append([]tools.Tool(nil), projectTools...)}
}

// Tools combines session built-ins, host-owned project-fact tools, and
// project LocalRegistry / external MCP tools without allowing name shadowing.
func (provider *runtimeToolProvider) Tools(context.Context) ([]tools.Tool, error) {
	if provider == nil || provider.runtime == nil || provider.session == nil {
		return nil, fmt.Errorf("runtime tool provider is incomplete")
	}
	facts := provider.runtime.facts
	if facts == nil {
		return nil, fmt.Errorf("project fact store is unavailable")
	}
	hostTools := tools.NewTools(provider.runtime.workspace)
	hostTools = append(hostTools, NewProjectFactTools(facts)...)
	seen := make(map[string]bool, len(hostTools)+len(provider.projectTools))
	for _, tool := range hostTools {
		seen[tool.Name()] = true
	}
	for _, projectTool := range provider.projectTools {
		if projectTool == nil {
			return nil, fmt.Errorf("project tool is nil")
		}
		if tools.IsName(projectTool.Name()) || seen[projectTool.Name()] {
			return nil, fmt.Errorf("tool name collision: %s", projectTool.Name())
		}
		seen[projectTool.Name()] = true
		hostTools = append(hostTools, projectTool)
	}
	return hostTools, nil
}
