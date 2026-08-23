package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"pentgo/internal/agent"
)

type providerFixture struct {
	tools    []agent.Tool
	err      error
	closeErr error
	closed   int
}

func (fixture *providerFixture) Tools(context.Context) ([]agent.Tool, error) {
	return append([]agent.Tool(nil), fixture.tools...), fixture.err
}

func (fixture *providerFixture) Close() error {
	fixture.closed++
	return fixture.closeErr
}

type namedTool struct{ name string }

func (tool namedTool) Name() string                                      { return tool.name }
func (namedTool) Description() string                                    { return "fixture" }
func (namedTool) Invoke(context.Context, map[string]any) (string, error) { return "", nil }

func TestCombineToolProvidersAggregatesToolsInOrder(t *testing.T) {
	first := &providerFixture{tools: []agent.Tool{namedTool{name: "amass"}}}
	second := &providerFixture{tools: []agent.Tool{namedTool{name: "httpx"}}}
	provider := combineToolProviders(first, nil, second)
	tools, err := provider.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := namesOfTools(tools); !reflect.DeepEqual(got, []string{"amass", "httpx"}) {
		t.Fatalf("tools = %v", got)
	}
	tools[0] = namedTool{name: "changed"}
	again, err := provider.Tools(context.Background())
	if err != nil || again[0].Name() != "amass" {
		t.Fatalf("defensive copy = %#v / %v", again, err)
	}
}

func TestCombineToolProvidersRejectsCollisionsAndNilTools(t *testing.T) {
	collision := combineToolProviders(
		&providerFixture{tools: []agent.Tool{namedTool{name: "httpx"}}},
		&providerFixture{tools: []agent.Tool{namedTool{name: "httpx"}}},
	)
	if _, err := collision.Tools(context.Background()); err == nil || err.Error() != "tool name collision: httpx" {
		t.Fatalf("collision err = %v", err)
	}
	nilTool := combineToolProviders(&providerFixture{tools: []agent.Tool{nil}})
	if _, err := nilTool.Tools(context.Background()); err == nil || err.Error() != "tool provider returned nil tool" {
		t.Fatalf("nil tool err = %v", err)
	}
}

func TestValidateProjectToolsRejectsSessionAndBackendCollisions(t *testing.T) {
	for _, test := range []struct {
		name            string
		toolName        string
		skillsAvailable bool
		wantFailure     bool
	}{
		{name: "local backend", toolName: "execute", wantFailure: true},
		{name: "session fact tool", toolName: "upsert_project_fact", wantFailure: true},
		{name: "active skill loader", toolName: "load_skill", skillsAvailable: true, wantFailure: true},
		{name: "inactive skill loader", toolName: "load_skill", skillsAvailable: false},
		{name: "ordinary tool", toolName: "custom_recon"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := combineToolProviders(&providerFixture{tools: []agent.Tool{namedTool{name: test.toolName}}})
			err := validateProjectTools(context.Background(), provider, test.skillsAvailable)
			if test.wantFailure && (err == nil || err.Error() != "tool name collision: "+test.toolName) {
				t.Fatalf("error = %v", err)
			}
			if !test.wantFailure && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCombineToolProvidersPropagatesToolAndCloseErrors(t *testing.T) {
	toolErr := errors.New("tools unavailable")
	provider := combineToolProviders(&providerFixture{err: toolErr})
	if _, err := provider.Tools(context.Background()); !errors.Is(err, toolErr) {
		t.Fatalf("tools err = %v", err)
	}
	firstErr := errors.New("first close")
	first := &providerFixture{closeErr: firstErr}
	second := &providerFixture{closeErr: errors.New("second close")}
	closable := combineToolProviders(first, second)
	closer := agent.ToolCloser(closable)
	if err := closer.Close(); !errors.Is(err, firstErr) {
		t.Fatalf("close err = %v", err)
	}
	if err := closer.Close(); !errors.Is(err, firstErr) || first.closed != 1 || second.closed != 1 {
		t.Fatalf("repeated close = %v; counts %d/%d", err, first.closed, second.closed)
	}
}

func namesOfTools(tools []agent.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}
