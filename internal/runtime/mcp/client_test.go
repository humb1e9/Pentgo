package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pentgo/internal/config"
	"pentgo/internal/runtime/evidence"

	"github.com/cloudwego/eino/components/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureInput struct {
	Value string `json:"value"`
	Fail  bool   `json:"fail,omitempty"`
}

func TestMain(m *testing.M) {
	if os.Getenv("PENTGO_MCP_FIXTURE") == "1" {
		runFixtureServer()
		return
	}
	os.Exit(m.Run())
}
func runFixtureServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-fixture", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "fixture_echo", Description: "Echo a local fixture value."}, func(_ context.Context, _ *sdk.CallToolRequest, input fixtureInput) (*sdk.CallToolResult, any, error) {
		if input.Fail {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture failure"}}, IsError: true}, nil, nil
		}
		value := input.Value
		if value == "ENV" {
			value = os.Getenv("FIXTURE_SECRET")
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture:" + value}}, StructuredContent: map[string]any{"echo": value}}, nil, nil
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		log.Print(err)
	}
}
func fixtureJournal(t *testing.T, secrets ...string) *evidence.Journal {
	t.Helper()
	journal, err := evidence.NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"), secrets...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal
}
func fixtureConfig() config.MCPConfig {
	return config.MCPConfig{Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_FIXTURE": "1"}}
}
func TestConnectStdioDiscoversAndInvokesToolWithEvidence(t *testing.T) {
	journal := fixtureJournal(t, "TOKEN")
	client, err := ConnectStdio(context.Background(), config.MCPConfig{Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_FIXTURE": "1", "FIXTURE_SECRET": "TOKEN"}}, journal, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tools := client.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools=%d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil || info.Name != "fixture_echo" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	output, err := tools[0].(tool.InvokableTool).InvokableRun(context.Background(), `{"value":"TOKEN"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "fixture:[redacted]") || !strings.Contains(output, "[evidence_ref: 1]") {
		t.Fatalf("output=%q", output)
	}
	record, ok := journal.Lookup(1)
	if !ok || !record.Success || record.Tool != "fixture_echo" || record.Output != output {
		t.Fatalf("record=%+v %t", record, ok)
	}
}
func TestMCPErrorsAndMalformedArgumentsAreSoftFailures(t *testing.T) {
	journal := fixtureJournal(t)
	client, err := ConnectStdio(context.Background(), fixtureConfig(), journal, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invokable := client.Tools()[0].(tool.InvokableTool)
	for index, args := range []string{`{"value":"TARGET","fail":true}`, `[`} {
		output, err := invokable.InvokableRun(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		record, ok := journal.Lookup(index + 1)
		if !ok || record.Success || record.Output != output || !strings.Contains(output, fmt.Sprintf("[evidence_ref: %d]", index+1)) {
			t.Fatalf("record=%+v output=%q", record, output)
		}
	}
}
func TestConnectStdioRejectsMissingDependencies(t *testing.T) {
	if _, err := ConnectStdio(context.Background(), config.MCPConfig{}, fixtureJournal(t), 1); err == nil {
		t.Fatal("missing command accepted")
	}
	if _, err := ConnectStdio(context.Background(), fixtureConfig(), nil, 1); err == nil {
		t.Fatal("nil journal accepted")
	}
}
