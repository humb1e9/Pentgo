package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"pentgo/internal/adapters/builtins"
	"pentgo/internal/agent"

	"github.com/cloudwego/eino/adk"
	filesystemmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
)

// Engine 为一次 transcript 回放构造短生命周期的 Eino ADK 智能体。
// 它仅持有适配器配置，领域状态和会话状态均保留在外部。
type Engine struct {
	model         model.ToolCallingChatModel
	tools         []agent.Tool
	workspace     *builtins.Workspace
	recordTool    ToolResultRecorder
	maxIterations int
}

// ToolResultRecorder 在内置工具输出返回模型前将其持久化，
// 与外部运行时工具的审计行为保持一致。
type ToolResultRecorder func(context.Context, string, map[string]any, bool, string) (string, error)

// NewEngine 校验默认工具集并保存适配器依赖。
func NewEngine(_ context.Context, chatModel model.ToolCallingChatModel, tools []agent.Tool, workspace *builtins.Workspace, recordTool ToolResultRecorder) (*Engine, error) {
	if chatModel == nil {
		return nil, fmt.Errorf("eino chat model is nil")
	}
	if err := validateTools(tools); err != nil {
		return nil, err
	}
	return &Engine{
		model:         chatModel,
		tools:         append([]agent.Tool(nil), tools...),
		workspace:     workspace,
		recordTool:    recordTool,
		maxIterations: 20,
	}, nil
}

// SetMaxIterations 仅修改适配器循环上限，不会在多次运行间保留 transcript 或领域状态。
func (engine *Engine) SetMaxIterations(max int) {
	if engine == nil {
		return
	}
	if max > 0 {
		engine.maxIterations = max
	}
}

// Run 根据持久化消息创建新的 ADK 智能体，并输出转换后的事件。
// 它不会在调用间保留 Provider 会话；进程重启后，回放是唯一的对话上下文来源。
func (engine *Engine) Run(ctx context.Context, input agent.TurnInput) (<-chan agent.TurnEvent, error) {
	if engine == nil || engine.model == nil {
		return nil, fmt.Errorf("eino engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tools := append([]agent.Tool(nil), engine.tools...)
	if input.Tools != nil {
		tools = input.Tools
	}
	if err := validateTools(tools); err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if builtins.IsName(tool.Name()) {
			return nil, fmt.Errorf("tool name conflicts with built-in tool: %s", tool.Name())
		}
	}
	einoTools, err := buildTools(tools)
	if err != nil {
		return nil, err
	}
	instruction := SystemPrompt(input.SystemPrompt, input.SkillSummary, input.ProjectFacts)
	maxIterations := input.MaxIterations
	if maxIterations <= 0 {
		maxIterations = engine.maxIterations
	}
	if maxIterations <= 0 {
		maxIterations = 20
	}
	handlers, err := engine.handlers(ctx)
	if err != nil {
		return nil, err
	}
	modelAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "pentgo-security-agent",
		Description: "渗透测试智能体",
		Instruction: instruction,
		Model:       engine.model,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: einoTools}},
		GenModelInput: func(_ context.Context, _ string, input *adk.AgentInput) ([]adk.Message, error) {
			messages := make([]adk.Message, 0, len(input.Messages)+1)
			messages = append(messages, schema.SystemMessage(instruction))
			messages = append(messages, input.Messages...)
			return messages, nil
		},
		MaxIterations: maxIterations,
		Handlers:      handlers,
	})
	if err != nil {
		return nil, fmt.Errorf("create Eino agent: %w", err)
	}
	adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: modelAgent, EnableStreaming: false})
	iterator := adkRunner.Run(ctx, toSchemaMessages(input.Messages))
	output := make(chan agent.TurnEvent, 16)
	go func() {
		defer close(output)
		for {
			event, ok := iterator.Next()
			if !ok {
				select {
				case output <- agent.TurnEvent{Kind: agent.TurnEventDone}:
				case <-ctx.Done():
				}
				return
			}
			if event == nil {
				continue
			}
			if event.Err != nil {
				select {
				case output <- agent.TurnEvent{Kind: agent.TurnEventError, Err: event.Err, Output: toolErrorMessage("模型", event.Err)}:
				case <-ctx.Done():
				}
				return
			}
			if event.Output == nil || event.Output.MessageOutput == nil {
				continue
			}
			message, err := event.Output.MessageOutput.GetMessage()
			if err != nil || message == nil {
				if err != nil {
					select {
					case output <- agent.TurnEvent{Kind: agent.TurnEventError, Err: err, Output: err.Error()}:
					case <-ctx.Done():
					}
					return
				}
				continue
			}
			converted := fromSchemaMessage(message)
			select {
			case output <- agent.TurnEvent{Kind: agent.TurnEventMessage, Message: converted, Output: assistantSummary(message.Content)}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return output, nil
}

// handlers 在启用工作区工具时附加它们及其证据装饰器。
func (engine *Engine) handlers(ctx context.Context) ([]adk.ChatModelAgentMiddleware, error) {
	if engine.workspace == nil {
		return nil, nil
	}
	filesystem, err := filesystemmiddleware.New(ctx, &filesystemmiddleware.MiddlewareConfig{Backend: engine.workspace, Shell: engine.workspace})
	if err != nil {
		return nil, fmt.Errorf("create filesystem middleware: %w", err)
	}
	return []adk.ChatModelAgentMiddleware{
		&builtinEvidenceHandler{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, record: engine.recordTool},
		filesystem,
	}, nil
}

// builtinEvidenceHandler 在本地后端工具执行后、ADK 智能体观察结果前记录工具输出。
type builtinEvidenceHandler struct {
	*adk.BaseChatModelAgentMiddleware
	record ToolResultRecorder
}

// WrapInvokableToolCall 保留原始工具结果，但将其替换为包含证据引用的持久化证据输出。
func (handler *builtinEvidenceHandler) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, toolContext *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	if handler == nil || handler.record == nil || toolContext == nil || !builtins.IsName(toolContext.Name) {
		return endpoint, nil
	}
	name := toolContext.Name
	return func(ctx context.Context, argumentsJSON string, options ...tool.Option) (string, error) {
		output, invokeErr := endpoint(ctx, argumentsJSON, options...)
		arguments := make(map[string]any)
		_ = json.Unmarshal([]byte(argumentsJSON), &arguments)
		if invokeErr != nil {
			output = invokeErr.Error()
		}
		return handler.record(context.Background(), name, arguments, invokeErr == nil, output)
	}, nil
}

// einoTool 通过 Eino 的可调用工具端口暴露内部 agent.Tool。
type einoTool struct {
	port agent.Tool
	info *schema.ToolInfo
}

// buildTools 将每个 Provider 无关工具转换为 Eino 工具元数据。
func buildTools(portsTools []agent.Tool) ([]tool.BaseTool, error) {
	result := make([]tool.BaseTool, 0, len(portsTools))
	for _, port := range portsTools {
		info, err := toolInfo(port)
		if err != nil {
			return nil, err
		}
		result = append(result, &einoTool{port: port, info: info})
	}
	return result, nil
}

// validateTools 在注册模型前拒绝 nil 工具或重复名称。
func validateTools(portsTools []agent.Tool) error {
	seen := make(map[string]bool, len(portsTools))
	for _, port := range portsTools {
		if port == nil {
			return fmt.Errorf("Eino tool is nil")
		}
		name := strings.TrimSpace(port.Name())
		if name == "" {
			return fmt.Errorf("Eino tool name is empty")
		}
		if seen[name] {
			return fmt.Errorf("Eino tool name collision: %s", name)
		}
		seen[name] = true
	}
	return nil
}

// toolInfo 保留提供的 JSON Schema；普通工具则使用对象 Schema，
// 确保全部 Provider 无关工具均可调用。
func toolInfo(port agent.Tool) (*schema.ToolInfo, error) {
	inputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	if provider, ok := port.(agent.ToolSchemaProvider); ok && provider.InputSchema() != nil {
		inputSchema = provider.InputSchema()
	}
	data, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, fmt.Errorf("encode tool %s schema: %w", port.Name(), err)
	}
	var converted einojsonschema.Schema
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, fmt.Errorf("decode tool %s schema: %w", port.Name(), err)
	}
	return &schema.ToolInfo{Name: port.Name(), Desc: port.Description(), ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&converted)}, nil
}

// Info 返回工具注册阶段准备好的不可变 Eino Schema。
func (adapter *einoTool) Info(context.Context) (*schema.ToolInfo, error) { return adapter.info, nil }

// InvokableRun 解析模型参数并传递给内部工具。
func (adapter *einoTool) InvokableRun(ctx context.Context, argumentsJSON string, _ ...tool.Option) (string, error) {
	arguments := make(map[string]any)
	if strings.TrimSpace(argumentsJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsJSON), &arguments); err != nil {
			return "", fmt.Errorf("decode %s arguments: %w", adapter.port.Name(), err)
		}
	}
	if arguments == nil {
		arguments = make(map[string]any)
	}
	return adapter.port.Invoke(ctx, arguments)
}

// toSchemaMessages 将持久化 transcript 消息转换为一次 ADK 回放所需格式。
func toSchemaMessages(messages []agent.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case agent.RoleSystem:
			result = append(result, schema.SystemMessage(message.Content))
		case agent.RoleUser:
			result = append(result, schema.UserMessage(message.Content))
		case agent.RoleTool:
			result = append(result, schema.ToolMessage(message.Content, message.ToolCallID, schema.WithToolName(message.ToolName)))
		case agent.RoleAssistant:
			calls := make([]schema.ToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				arguments := call.RawArguments
				if strings.TrimSpace(arguments) == "" {
					data, _ := json.Marshal(call.Arguments)
					arguments = string(data)
				}
				calls = append(calls, schema.ToolCall{ID: call.ID, Function: schema.FunctionCall{Name: call.Name, Arguments: arguments}})
			}
			result = append(result, schema.AssistantMessage(message.Content, calls))
		default:
			result = append(result, &schema.Message{Role: schema.RoleType(message.Role), Content: message.Content})
		}
	}
	return result
}

// fromSchemaMessage 保留工具调用 ID 和格式错误的原始参数，
// 使 transcript 如实记录 Provider 的请求内容。
func fromSchemaMessage(message *schema.Message) agent.Message {
	converted := agent.Message{Role: string(message.Role), Content: message.Content, ToolCallID: message.ToolCallID, ToolName: message.ToolName}
	converted.ToolArguments = cloneMap(message.Extra)
	for _, call := range message.ToolCalls {
		arguments := make(map[string]any)
		_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
		converted.ToolCalls = append(converted.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments, RawArguments: call.Function.Arguments})
	}
	if converted.Role == "" {
		converted.Role = agent.RoleAssistant
	}
	return converted
}

// cloneMap 避免 Provider 消息转换过程共享可变参数。
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

var _ tool.InvokableTool = (*einoTool)(nil)
