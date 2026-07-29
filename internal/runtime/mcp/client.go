package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"pentgo/internal/config"
	"pentgo/internal/runtime/evidence"
)

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	session        *sdk.ClientSession
	journal        *evidence.Journal
	tools          []tool.BaseTool
	maxOutputBytes int
	closeOnce      sync.Once
	closeErr       error
}

type remoteTool struct {
	client *Client
	info   *schema.ToolInfo
	name   string
}

func ConnectStdio(ctx context.Context, cfg config.MCPConfig, journal *evidence.Journal, maxOutputBytes int) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("MCP command is empty")
	}
	if journal == nil {
		return nil, fmt.Errorf("MCP evidence journal is nil")
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = 65536
	}
	command := osexec.Command(cfg.Command, cfg.Args...)
	command.Env = mergeEnv(os.Environ(), cfg.Env)
	protocolClient := sdk.NewClient(&sdk.Implementation{Name: "pentgo", Version: "1.0.0"}, nil)
	session, err := protocolClient.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect stdio MCP: %w", err)
	}
	client := &Client{session: session, journal: journal, maxOutputBytes: maxOutputBytes}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	seen := make(map[string]bool, len(listed.Tools))
	for _, definition := range listed.Tools {
		name := strings.TrimSpace(definition.Name)
		if !toolNamePattern.MatchString(name) {
			_ = session.Close()
			return nil, fmt.Errorf("invalid MCP tool name: %q", definition.Name)
		}
		if seen[name] {
			_ = session.Close()
			return nil, fmt.Errorf("duplicate MCP tool name: %s", name)
		}
		seen[name] = true
		params, err := convertSchema(definition.InputSchema)
		if err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("convert MCP tool %s schema: %w", name, err)
		}
		info := &schema.ToolInfo{Name: name, Desc: definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)}
		client.tools = append(client.tools, &remoteTool{client: client, info: info, name: name})
	}
	return client, nil
}

func convertSchema(input any) (*einojsonschema.Schema, error) {
	if input == nil {
		input = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var converted einojsonschema.Schema
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, err
	}
	return &converted, nil
}
func mergeEnv(inherited []string, overrides map[string]string) []string {
	values := make(map[string]string, len(inherited)+len(overrides))
	for _, entry := range inherited {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		if key != "" {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
func (client *Client) Tools() []tool.BaseTool {
	if client == nil {
		return nil
	}
	return append([]tool.BaseTool(nil), client.tools...)
}
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.closeOnce.Do(func() { client.closeErr = client.session.Close() })
	return client.closeErr
}
func (remote *remoteTool) Info(context.Context) (*schema.ToolInfo, error) { return remote.info, nil }
func (remote *remoteTool) InvokableRun(ctx context.Context, argumentsJSON string, _ ...tool.Option) (string, error) {
	startedAt := time.Now().UTC()
	arguments := make(map[string]any)
	if err := json.Unmarshal([]byte(argumentsJSON), &arguments); err != nil || arguments == nil {
		return remote.record(map[string]any{"_raw": argumentsJSON}, false, "invalid MCP tool arguments: "+argumentsJSON, startedAt)
	}
	result, err := remote.client.session.CallTool(ctx, &sdk.CallToolParams{Name: remote.name, Arguments: arguments})
	success := err == nil && result != nil && !result.IsError
	output := ""
	if err != nil {
		output = "MCP tool call failed: " + err.Error()
	} else {
		output = renderMCPResult(result)
	}
	return remote.record(arguments, success, output, startedAt)
}
func (remote *remoteTool) record(arguments any, success bool, output string, started time.Time) (string, error) {
	record, err := remote.client.journal.Record(remote.name, arguments, success, boundText(output, remote.client.maxOutputBytes), started, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return record.Output, nil
}
func renderMCPResult(result *sdk.CallToolResult) string {
	if result == nil {
		return "MCP tool returned no content"
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, content := range result.Content {
		if content == nil {
			parts = append(parts, "MCP tool returned empty content")
			continue
		}
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		data, err := content.MarshalJSON()
		if err != nil {
			parts = append(parts, "MCP content encoding failed: "+err.Error())
		} else {
			parts = append(parts, string(data))
		}
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			parts = append(parts, "MCP structured content encoding failed: "+err.Error())
		} else {
			parts = append(parts, "structured: "+string(data))
		}
	}
	if len(parts) == 0 {
		return "MCP tool returned no content"
	}
	return strings.Join(parts, "\n")
}
func boundText(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "\n[output truncated]"
}
