package mcp

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/config"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fixtureInput 是进程内 HTTP/SSE 夹具和启动的 stdio 夹具共用的最小 Schema，
// 用于验证 MCP Schema 透传和参数转发。
type fixtureInput struct {
	Value string `json:"value"`
}

func TestMCPAdapterConnectsHTTPAndSSEServers(t *testing.T) {
	streamable := fixtureServer("http_scan", "http:")
	httpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return streamable }, nil)
	httpServer := httptest.NewServer(requireHeader(t, httpHandler))
	defer httpServer.Close()

	sse := fixtureServer("sse_scan", "sse:")
	sseServer := httptest.NewServer(requireHeader(t, sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return sse }, nil)))
	defer sseServer.Close()

	evidence, err := storage.OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer evidence.Close()
	clients, err := ConnectAll(context.Background(), config.MCPServers{
		"remote": {Type: "http", URL: httpServer.URL, Headers: map[string]string{"X-Fixture": "TOKEN"}},
		"legacy": {Type: "sse", URL: sseServer.URL, Headers: map[string]string{"X-Fixture": "TOKEN"}},
	}, evidence, 1024, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close()
	tools, err := clients.Tools(context.Background())
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools/err = %#v/%v", tools, err)
	}
	byName := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	for name, expected := range map[string]string{"http_scan": "http:TARGET", "sse_scan": "sse:TARGET"} {
		output, invokeErr := byName[name].Invoke(context.Background(), map[string]any{"value": "TARGET"})
		if invokeErr != nil || !strings.Contains(output, expected) {
			t.Fatalf("%s output/err = %q/%v", name, output, invokeErr)
		}
	}
}

// fixtureServer 为传输适配器测试创建确定性的 MCP 服务。
func fixtureServer(toolName, prefix string) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-fixture", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: toolName, Description: "Fixture scan."}, func(_ context.Context, _ *sdk.CallToolRequest, input fixtureInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: prefix + input.Value}}}, nil, nil
	})
	return server
}

// requireHeader 校验配置的 HTTP/SSE 认证请求头已送达。
func requireHeader(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Fixture") != "TOKEN" {
			t.Errorf("headers = %#v", request.Header)
		}
		next.ServeHTTP(writer, request)
	})
}

// TestMain 在设置夹具环境变量时将测试二进制文件作为 stdio MCP 子进程运行；
// 常规测试执行仍会运行下方测试套件。
func TestMain(m *testing.M) {
	if os.Getenv("PENTGO_MCP_ADAPTER_FIXTURE") == "1" {
		toolName := os.Getenv("PENTGO_MCP_ADAPTER_TOOL_NAME")
		if toolName == "" {
			toolName = "fixture_echo"
		}
		prefix := os.Getenv("PENTGO_MCP_ADAPTER_PREFIX")
		if prefix == "" {
			prefix = "fixture:"
		}
		server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-fixture", Version: "1.0.0"}, nil)
		sdk.AddTool(server, &sdk.Tool{Name: toolName, Description: "Echo a fixture value."}, func(_ context.Context, _ *sdk.CallToolRequest, input fixtureInput) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: prefix + input.Value}}}, nil, nil
		})
		if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			log.Print(err)
		}
		return
	}
	os.Exit(m.Run())
}

func TestMCPAdapterAggregatesNamedServers(t *testing.T) {
	evidence, err := storage.OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer evidence.Close()
	clients, err := ConnectAll(context.Background(), config.MCPServers{
		"browser": {Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_ADAPTER_FIXTURE": "1", "PENTGO_MCP_ADAPTER_TOOL_NAME": "browser_scan", "PENTGO_MCP_ADAPTER_PREFIX": "browser:"}},
		"nmap":    {Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_ADAPTER_FIXTURE": "1", "PENTGO_MCP_ADAPTER_TOOL_NAME": "nmap_scan", "PENTGO_MCP_ADAPTER_PREFIX": "nmap:"}},
	}, evidence, 1024, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer clients.Close()
	tools, err := clients.Tools(context.Background())
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools/err = %#v/%v", tools, err)
	}
	byName := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	for name, expected := range map[string]string{"browser_scan": "browser:TARGET", "nmap_scan": "nmap:TARGET"} {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("tool %q missing from %#v", name, byName)
		}
		output, invokeErr := tool.Invoke(context.Background(), map[string]any{"value": "TARGET"})
		if invokeErr != nil || !strings.Contains(output, expected) {
			t.Fatalf("%s output/err = %q/%v", name, output, invokeErr)
		}
	}
}

func TestMCPAdapterRejectsDuplicateToolNamesAcrossServers(t *testing.T) {
	evidence, err := storage.OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer evidence.Close()
	_, err = ConnectAll(context.Background(), config.MCPServers{
		"first":  {Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_ADAPTER_FIXTURE": "1", "PENTGO_MCP_ADAPTER_TOOL_NAME": "scan"}},
		"second": {Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_ADAPTER_FIXTURE": "1", "PENTGO_MCP_ADAPTER_TOOL_NAME": "scan"}},
	}, evidence, 1024, "", "")
	if err == nil || !strings.Contains(err.Error(), "duplicate MCP tool name across servers") {
		t.Fatalf("error = %v", err)
	}
}

func TestMCPAdapterReturnsPortsTool(t *testing.T) {
	evidence, err := storage.OpenEvidenceStore(filepath.Join(t.TempDir(), "pentgo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer evidence.Close()
	client, err := ConnectStdio(context.Background(), config.MCPConfig{Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_ADAPTER_FIXTURE": "1"}}, evidence, 1024, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
	if _, ok := tools[0].(agent.Tool); !ok {
		t.Fatalf("tool does not implement agent.Tool: %T", tools[0])
	}
	output, err := tools[0].Invoke(context.Background(), map[string]any{"value": "TARGET"})
	if err != nil || !strings.Contains(output, "fixture:TARGET") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, ok := tools[0].(agent.ToolSchemaProvider); !ok {
		t.Fatalf("tool does not expose schema: %T", tools[0])
	}
}
