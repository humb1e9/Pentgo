package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/config"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolNamePattern 保证服务端和工具标识可在全部支持的 MCP 传输方式中使用，
// 并可安全用于稳定路由。
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Client 持有一个已初始化的 MCP 客户端会话及其发现的工具。
type Client struct {
	session        *sdk.ClientSession
	tools          []agent.Tool
	maxOutputBytes int
	closeOnce      sync.Once
	closeErr       error
}

// remoteTool 将内部 Tool 调用转发至一个 MCP 服务端会话。
type remoteTool struct {
	client      *Client
	name        string
	description string
	schema      map[string]any
}

// Clients 持有一个项目运行时配置的全部 MCP 连接，并聚合每个具名连接。
type Clients struct {
	clients   map[string]*Client
	tools     []agent.Tool
	closeOnce sync.Once
	closeErr  error
}

// ConfigSecrets 提取配置的命令环境变量值和 HTTP 请求头，
// 供证据存储在持久化工具输出时脱敏。
func ConfigSecrets(configurations config.MCPServers) []string {
	secrets := make([]string, 0)
	names := make([]string, 0, len(configurations))
	for name := range configurations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		configuration := configurations[name]
		for _, value := range configuration.Env {
			if strings.TrimSpace(value) != "" {
				secrets = append(secrets, value)
			}
		}
		for _, value := range configuration.Headers {
			if strings.TrimSpace(value) != "" {
				secrets = append(secrets, value)
			}
		}
	}
	return secrets
}

// ConnectAll 启动全部具名 MCP 服务。远端工具名称必须在已配置服务中全局唯一，
// 并以原名称暴露给模型。
func ConnectAll(ctx context.Context, configurations config.MCPServers, evidence *storage.EvidenceStore, maxOutputBytes int, projectRoot, tmpDir string) (*Clients, error) {
	if len(configurations) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(configurations))
	for name := range configurations {
		if !toolNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid MCP server name: %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	clients := &Clients{clients: make(map[string]*Client, len(names))}
	seenTools := make(map[string]bool)
	for _, name := range names {
		client, err := Connect(ctx, configurations[name], evidence, maxOutputBytes, projectRoot, tmpDir)
		if err != nil {
			_ = clients.Close()
			return nil, fmt.Errorf("connect MCP server %s: %w", name, err)
		}
		clients.clients[name] = client
		tools, err := client.Tools(ctx)
		if err != nil {
			_ = clients.Close()
			return nil, fmt.Errorf("list MCP server %s tools: %w", name, err)
		}
		for _, inner := range tools {
			toolName := inner.Name()
			if seenTools[toolName] {
				_ = clients.Close()
				return nil, fmt.Errorf("duplicate MCP tool name across servers: %s", toolName)
			}
			seenTools[toolName] = true
			clients.tools = append(clients.tools, inner)
		}
	}
	return clients, nil
}

// ConnectStdio 为显式需要本地标准输入输出服务的调用方保留。
// ConnectStdio 使用项目专属路径启动一个本地 MCP 服务进程。
func ConnectStdio(ctx context.Context, cfg config.MCPConfig, evidence *storage.EvidenceStore, maxOutputBytes int, projectRoot, tmpDir string) (*Client, error) {
	if cfg.Transport() != "stdio" {
		return nil, fmt.Errorf("MCP transport is %q, not stdio", cfg.Transport())
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("MCP command is empty")
	}
	if evidence == nil {
		return nil, fmt.Errorf("MCP evidence store is nil")
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = 65536
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := osexec.Command(cfg.Command, cfg.Args...)
	command.Env = mergeEnv(os.Environ(), cfg.Env)
	if strings.TrimSpace(tmpDir) != "" {
		resolvedTmp, err := filepath.Abs(tmpDir)
		if err != nil {
			return nil, fmt.Errorf("resolve MCP temporary directory: %w", err)
		}
		if err := os.MkdirAll(resolvedTmp, 0o700); err != nil {
			return nil, fmt.Errorf("create MCP temporary directory: %w", err)
		}
		command.Dir = resolvedTmp
		environment := map[string]string{"PENTGO_PROJECT_TMP": resolvedTmp, "TMPDIR": resolvedTmp, "TMP": resolvedTmp, "TEMP": resolvedTmp}
		if strings.TrimSpace(projectRoot) != "" {
			resolvedRoot, err := filepath.Abs(projectRoot)
			if err != nil {
				return nil, fmt.Errorf("resolve MCP project directory: %w", err)
			}
			environment["PENTGO_PROJECT_ROOT"] = resolvedRoot
		}
		command.Env = mergeEnv(command.Env, environment)
	}
	return connectTransport(ctx, &sdk.CommandTransport{Command: command}, evidence, maxOutputBytes)
}

// Connect 根据配置的传输方式打开一个 MCP 服务。
// Connect 在标准输入输出、HTTP 和旧版 SSE 传输方式之间选择。
func Connect(ctx context.Context, cfg config.MCPConfig, evidence *storage.EvidenceStore, maxOutputBytes int, projectRoot, tmpDir string) (*Client, error) {
	switch cfg.Transport() {
	case "stdio":
		return ConnectStdio(ctx, cfg, evidence, maxOutputBytes, projectRoot, tmpDir)
	case "http":
		return ConnectHTTP(ctx, cfg, evidence, maxOutputBytes)
	case "sse":
		return ConnectSSE(ctx, cfg, evidence, maxOutputBytes)
	default:
		return nil, fmt.Errorf("unsupported MCP transport: %q", cfg.Transport())
	}
}

// ConnectHTTP 使用配置的请求头打开 Streamable HTTP MCP 会话。
func ConnectHTTP(ctx context.Context, cfg config.MCPConfig, evidence *storage.EvidenceStore, maxOutputBytes int) (*Client, error) {
	endpoint, err := validEndpoint(cfg.URL)
	if err != nil {
		return nil, err
	}
	return connectTransport(ctx, &sdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient(cfg.Headers)}, evidence, maxOutputBytes)
}

// ConnectSSE 使用配置的请求头打开旧版 SSE MCP 会话。
func ConnectSSE(ctx context.Context, cfg config.MCPConfig, evidence *storage.EvidenceStore, maxOutputBytes int) (*Client, error) {
	endpoint, err := validEndpoint(cfg.URL)
	if err != nil {
		return nil, err
	}
	return connectTransport(ctx, &sdk.SSEClientTransport{Endpoint: endpoint, HTTPClient: httpClient(cfg.Headers)}, evidence, maxOutputBytes)
}

// connectTransport 初始化共享的 MCP 协议会话，并一次性获取工具目录。
// 初始化失败时通过 SDK 会话关闭底层传输。
func connectTransport(ctx context.Context, transport sdk.Transport, evidence *storage.EvidenceStore, maxOutputBytes int) (*Client, error) {
	if evidence == nil {
		return nil, fmt.Errorf("MCP evidence store is nil")
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = 65536
	}
	if ctx == nil {
		ctx = context.Background()
	}
	protocolClient := sdk.NewClient(&sdk.Implementation{Name: "pentgo", Version: "1.0.0"}, nil)
	session, err := protocolClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP: %w", err)
	}
	client := &Client{session: session, maxOutputBytes: maxOutputBytes}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	seen := make(map[string]bool, len(listed.Tools))
	for _, definition := range listed.Tools {
		name := strings.TrimSpace(definition.Name)
		if !toolNamePattern.MatchString(name) {
			_ = client.Close()
			return nil, fmt.Errorf("invalid MCP tool name: %q", definition.Name)
		}
		if seen[name] {
			_ = client.Close()
			return nil, fmt.Errorf("duplicate MCP tool name: %s", name)
		}
		seen[name] = true
		inputSchema := make(map[string]any)
		if definition.InputSchema != nil {
			data, marshalErr := json.Marshal(definition.InputSchema)
			if marshalErr != nil || json.Unmarshal(data, &inputSchema) != nil {
				_ = client.Close()
				return nil, fmt.Errorf("convert MCP tool %s schema", name)
			}
		}
		client.tools = append(client.tools, &remoteTool{client: client, name: name, description: definition.Description, schema: inputSchema})
	}
	return client, nil
}

// validEndpoint 校验远端传输支持的 HTTP(S) 端点。
func validEndpoint(value string) (string, error) {
	endpoint := strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("MCP URL must be an absolute http or https URL")
	}
	return parsed.String(), nil
}

// httpClient 包装默认传输层以追加静态 MCP 认证请求头。
func httpClient(headers map[string]string) *http.Client {
	base := http.DefaultTransport
	if transport, ok := base.(*http.Transport); ok {
		cloned := transport.Clone()
		cloned.Proxy = http.ProxyFromEnvironment
		base = cloned
	}
	return &http.Client{Transport: headerTransport{base: base, headers: headers}}
}

// headerTransport 注入配置请求头，但不修改调用方请求。
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

// RoundTrip 在添加配置值前复制请求头映射。
func (transport headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	for name, value := range transport.headers {
		copy.Header.Set(name, value)
	}
	return transport.base.RoundTrip(copy)
}

// Tools 返回 MCP 初始化期间发现的工具副本。
func (client *Client) Tools(context.Context) ([]agent.Tool, error) {
	if client == nil {
		return nil, fmt.Errorf("MCP client is nil")
	}
	return append([]agent.Tool(nil), client.tools...), nil
}

// Close 仅释放一次 MCP 会话。
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.closeOnce.Do(func() {
		if client.session != nil {
			client.closeErr = client.session.Close()
		}
	})
	return client.closeErr
}

// Tools 返回 ConnectAll 校验全局唯一性后的聚合工具副本。
func (clients *Clients) Tools(context.Context) ([]agent.Tool, error) {
	if clients == nil {
		return nil, fmt.Errorf("MCP clients are nil")
	}
	return append([]agent.Tool(nil), clients.tools...), nil
}

// Close 仅释放一次全部具名客户端，并返回首个关闭错误。
func (clients *Clients) Close() error {
	if clients == nil {
		return nil
	}
	clients.closeOnce.Do(func() {
		names := make([]string, 0, len(clients.clients))
		for name := range clients.clients {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := clients.clients[name].Close(); clients.closeErr == nil && err != nil {
				clients.closeErr = err
			}
		}
	})
	return clients.closeErr
}

// Name 返回用于全局路由的原始 MCP 工具名称。
func (remote *remoteTool) Name() string { return remote.name }

// Description 返回 MCP 服务提供的工具描述。
func (remote *remoteTool) Description() string { return remote.description }

// InputSchema 返回 MCP 服务 Schema 的独立深拷贝。
func (remote *remoteTool) InputSchema() map[string]any {
	result := make(map[string]any, len(remote.schema))
	for key, value := range remote.schema {
		result[key] = value
	}
	return result
}

// Invoke 将参数转发至 MCP，并在应用层装饰器记录证据前限制返回文本大小。
func (remote *remoteTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := remote.client.session.CallTool(ctx, &sdk.CallToolParams{Name: remote.name, Arguments: arguments})
	if err != nil {
		return "MCP 工具调用失败：" + err.Error(), err
	}
	output := boundText(renderMCPResult(result), remote.client.maxOutputBytes)
	if result == nil || result.IsError {
		return output, fmt.Errorf("MCP tool %s returned an error", remote.name)
	}
	return output, nil
}

// renderMCPResult 将全部文本和资源类型的 MCP 内容扁平化后提供给模型。
func renderMCPResult(result *sdk.CallToolResult) string {
	if result == nil {
		return "MCP 工具没有返回内容"
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, content := range result.Content {
		if content == nil {
			parts = append(parts, "MCP 工具返回空内容")
			continue
		}
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		data, err := content.MarshalJSON()
		if err != nil {
			parts = append(parts, "MCP 内容编码失败："+err.Error())
		} else {
			parts = append(parts, string(data))
		}
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			parts = append(parts, "MCP 结构化内容编码失败："+err.Error())
		} else {
			parts = append(parts, "structured: "+string(data))
		}
	}
	if len(parts) == 0 {
		return "MCP 工具没有返回内容"
	}
	return boundText(strings.Join(parts, "\n"), 65536)
}

// boundText 按字节上限截断文本，并为模型上下文标记截断结果。
func boundText(value string, maximum int) string {
	if maximum <= 0 {
		return value
	}
	if len(value) <= maximum && utf8.ValidString(value) {
		return value
	}
	end := maximum
	if end > len(value) {
		end = len(value)
	}
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "\n[输出已截断]"
}

// mergeEnv 将配置的进程变量覆盖到继承的环境变量中。
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

var _ agent.ToolProvider = (*Client)(nil)
var _ agent.ToolCloser = (*Client)(nil)
var _ agent.ToolProvider = (*Clients)(nil)
var _ agent.ToolCloser = (*Clients)(nil)
var _ agent.Tool = (*remoteTool)(nil)
var _ agent.ToolSchemaProvider = (*remoteTool)(nil)
