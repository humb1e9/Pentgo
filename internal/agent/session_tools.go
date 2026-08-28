package agent

import (
	"context"
	"fmt"

	"pentgo/internal/core"
	sessionstate "pentgo/internal/project/session"
	projectturn "pentgo/internal/project/turn"
	builtins "pentgo/internal/tools"
)

// runtimeToolProvider builds tools visible to one session turn.
type runtimeToolProvider struct {
	runtime      *ProjectRuntime
	session      *sessionstate.Session
	projectTools []core.Tool
}

func newRuntimeToolProvider(runtime *ProjectRuntime, session *sessionstate.Session, projectTools []core.Tool) *runtimeToolProvider {
	return &runtimeToolProvider{runtime: runtime, session: session, projectTools: append([]core.Tool(nil), projectTools...)}
}

// Tools combines session built-ins, host-owned project-fact tools, and
// project LocalRegistry / external MCP tools without allowing name shadowing.
func (provider *runtimeToolProvider) Tools(context.Context) ([]core.Tool, error) {
	if provider == nil || provider.runtime == nil || provider.session == nil {
		return nil, fmt.Errorf("runtime tool provider is incomplete")
	}
	facts := provider.runtime.ProjectFacts()
	if facts == nil {
		return nil, fmt.Errorf("project fact store is unavailable")
	}
	tools := builtins.NewTools(provider.runtime.Workspace())
	tools = append(tools, newProjectFactTools(facts)...)
	seen := make(map[string]bool, len(tools)+len(provider.projectTools))
	for _, tool := range tools {
		seen[tool.Name()] = true
	}
	for _, projectTool := range provider.projectTools {
		if projectTool == nil {
			return nil, fmt.Errorf("project tool is nil")
		}
		if builtins.IsName(projectTool.Name()) || seen[projectTool.Name()] {
			return nil, fmt.Errorf("tool name collision: %s", projectTool.Name())
		}
		seen[projectTool.Name()] = true
		tools = append(tools, projectTool)
	}
	return tools, nil
}

func newProjectFactTools(facts *projectturn.ProjectFactLedger) []core.Tool {
	return projectturn.NewProjectFactTools(facts)
}
