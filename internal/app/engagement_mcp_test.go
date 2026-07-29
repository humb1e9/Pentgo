package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/config"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type appFixtureInput struct {
	Value string `json:"value"`
}

func TestMain(m *testing.M) {
	if os.Getenv("PENTGO_APP_MCP_FIXTURE") == "1" {
		server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-app-fixture", Version: "1.0.0"}, nil)
		sdk.AddTool(server, &sdk.Tool{Name: "fixture_echo", Description: "Echo a local fixture value."}, func(_ context.Context, _ *sdk.CallToolRequest, input appFixtureInput) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture:" + input.Value}}}, map[string]string{"echo": input.Value}, nil
		})
		if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			log.Print(err)
		}
		return
	}
	os.Exit(m.Run())
}

type sequenceToolModel struct {
	turns []*schema.Message
	calls int
	tools []*schema.ToolInfo
}

func (m *sequenceToolModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.calls >= len(m.turns) {
		return schema.AssistantMessage("", nil), nil
	}
	message := m.turns[m.calls]
	m.calls++
	return message, nil
}
func (m *sequenceToolModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported")
}
func (m *sequenceToolModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.tools = append([]*schema.ToolInfo(nil), tools...)
	return m, nil
}
func toolCall(name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: name + "-call", Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

func TestServiceRunsMCPStdioToolEndToEnd(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.MCP = &config.MCPConfig{Command: os.Args[0], Env: map[string]string{"PENTGO_APP_MCP_FIXTURE": "1", "FIXTURE_SECRET": "TOKEN"}}
	fixture := &sequenceToolModel{turns: []*schema.Message{toolCall("fixture_echo", `{"value":"TARGET"}`), toolCall("record_finding", `{"title":"MCP fixture finding","severity":"medium","description":"The fixture returned TARGET.","evidence_refs":[1],"recommendation":"Apply fixture control."}`), schema.AssistantMessage("MCP fixture complete.", nil)}}
	service := NewService(cfg, Dependencies{NewEngagementID: func(time.Time) (string, error) { return "eng-mcp", nil }, NewEinoModel: func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) { return fixture, nil }})
	result, err := service.Run(context.Background(), Request{Target: sess.Target{Canonical: "https://fixture.local"}, Intent: "TASK", OutputRoot: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunError != nil || result.Session.Status != sess.SessionDone || len(result.Session.Findings) != 1 || result.Session.FinalSummary != "MCP fixture complete." {
		t.Fatalf("result=%+v", result)
	}
	evidence, err := os.ReadFile(result.Artifacts.EvidenceJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), `"tool":"fixture_echo"`) || strings.Contains(string(evidence), "record_finding") {
		t.Fatalf("evidence=%s", evidence)
	}
	report, err := os.ReadFile(result.Artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(report), "MCP fixture finding") >= strings.Index(string(report), "## Agent Summary") {
		t.Fatalf("report=%s", report)
	}
}

func TestServicePublishesMCPInitializationFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.MCP = &config.MCPConfig{Command: filepath.Join(t.TempDir(), "missing-mcp")}
	service := NewService(cfg, Dependencies{NewEngagementID: func(time.Time) (string, error) { return "eng-mcp-error", nil }})
	result, err := service.Run(context.Background(), Request{Target: sess.Target{Canonical: "https://fixture.local"}, Intent: "TASK", OutputRoot: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunError == nil || result.Session.Status != sess.SessionFailed || result.Session.StopReason != "mcp_init_error" {
		t.Fatalf("result=%+v", result)
	}
	data, readErr := os.ReadFile(result.Artifacts.EvidenceJSONL)
	if readErr != nil || len(data) != 0 {
		t.Fatalf("evidence=%q err=%v", data, readErr)
	}
}
