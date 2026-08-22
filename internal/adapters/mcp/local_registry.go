package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"pentgo/internal/agent"
	"pentgo/internal/config"
)

// LocalRegistry exposes user-configured local CLI tools through the same
// agent.Tool abstraction used by external MCP clients. It does not inspect,
// resolve, or identity-check commands: users own their command configuration.
type LocalRegistry struct {
	tools []agent.Tool
}

// NewLocalRegistry registers every entry in configurations. Registration is
// configuration-only: a configured command is invoked later through the normal
// tool call path, so an unavailable command fails as that call's tool result.
func NewLocalRegistry(configurations config.LocalTools, maximumOutputBytes int) *LocalRegistry {
	if maximumOutputBytes <= 0 {
		maximumOutputBytes = 65536
	}
	names := make([]string, 0, len(configurations))
	for name := range configurations {
		names = append(names, name)
	}
	sort.Strings(names)

	registry := &LocalRegistry{tools: make([]agent.Tool, 0, len(names))}
	for _, name := range names {
		configuration := configurations[name]
		description := strings.TrimSpace(configuration.Description)
		if description == "" {
			description = "调用用户配置的本机 CLI " + name + " 执行原生参数。args 的每项都是一个独立命令行参数。"
		}
		registry.tools = append(registry.tools, &localTool{name: name, description: description, command: strings.TrimSpace(configuration.Command), maximumOutputBytes: maximumOutputBytes})
	}
	return registry
}

// Tools returns a defensive copy of the configured local tools.
func (registry *LocalRegistry) Tools(context.Context) ([]agent.Tool, error) {
	if registry == nil {
		return nil, nil
	}
	return append([]agent.Tool(nil), registry.tools...), nil
}

type localTool struct {
	name               string
	description        string
	command            string
	maximumOutputBytes int
}

func (tool *localTool) Name() string        { return tool.name }
func (tool *localTool) Description() string { return tool.description }

func (*localTool) InputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"args"},
		"properties": map[string]any{
			"args": map[string]any{
				"type":        "array",
				"description": "传给本机 CLI 的原生参数数组；每项必须是一个独立 argv 元素。",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}

func (tool *localTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	args, err := stringArguments(arguments)
	if err != nil {
		return "参数无效：" + err.Error(), err
	}
	output, err := runLocalCommand(ctx, tool.command, args, tool.maximumOutputBytes)
	if err != nil {
		return output, fmt.Errorf("%s: %w", tool.name, err)
	}
	return output, nil
}

func stringArguments(arguments map[string]any) ([]string, error) {
	if arguments == nil {
		return nil, fmt.Errorf("args is required")
	}
	value, ok := arguments["args"]
	if !ok {
		return nil, fmt.Errorf("args is required")
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		args := make([]string, 0, len(values))
		for index, value := range values {
			argument, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("args[%d] must be a string", index)
			}
			args = append(args, argument)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("args must be an array of strings")
	}
}

func runLocalCommand(ctx context.Context, commandName string, args []string, maximumOutputBytes int) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, commandName, args...)
	configureLocalCommand(command)
	output := &boundedBuffer{maximum: maximumOutputBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.String(), err
}

type boundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.maximum <= 0 {
		buffer.maximum = 65536
	}
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			_, _ = buffer.buffer.Write(value[:remaining])
			buffer.truncated = true
		} else {
			_, _ = buffer.buffer.Write(value)
		}
	} else if len(value) > 0 {
		buffer.truncated = true
	}
	return len(value), nil
}

func (buffer *boundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	value := buffer.buffer.String()
	if buffer.truncated {
		return utf8Prefix(value, buffer.maximum) + "\n[输出已截断]"
	}
	if !utf8.ValidString(value) {
		return boundText(value, buffer.maximum)
	}
	return value
}

func utf8Prefix(value string, maximum int) string {
	if maximum <= 0 {
		return value
	}
	end := maximum
	if end > len(value) {
		end = len(value)
	}
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

var _ agent.ToolProvider = (*LocalRegistry)(nil)
var _ agent.Tool = (*localTool)(nil)
var _ agent.ToolSchemaProvider = (*localTool)(nil)
