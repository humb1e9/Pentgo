package agent

import (
	"context"
	"fmt"
	"net/http"

	einoclaude "github.com/cloudwego/eino-ext/components/model/claude"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// einoAnthropicMaxTokens is the default response token cap for the claude ADK
// model. claude.Config.MaxTokens is mandatory (unlike the openai provider), and
// the legacy text AnthropicClient hard-coded 1024 — too small for a code-writing
// engagement agent. 4096 is a safe default; make it configurable later if needed.
const einoAnthropicMaxTokens = 4096

// NewEinoOpenAIModel 从 PentGo 的 ProviderConfig 构造一个满足 Eino
// model.ToolCallingChatModel 的 OpenAI 原生 tool-call 模型。用于 openai 路径的
// ADK 单代理循环；anthropic 路径仍走文本协议 Client（见 anthropic.go）。
//
// 不预绑定工具：ADK ChatModelAgent 内部会调 WithTools 注入工具 schema。
func NewEinoOpenAIModel(ctx context.Context, config ProviderConfig, client *http.Client, lookupEnv func(string) (string, bool)) (model.ToolCallingChatModel, error) {
	key, err := apiKey(config, defaultEnvLookup(lookupEnv))
	if err != nil {
		return nil, err
	}
	baseURL, err := einoBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	chatModel, err := einoopenai.NewChatModel(requestContext(ctx), &einoopenai.ChatModelConfig{
		APIKey:     key,
		BaseURL:    baseURL,
		Model:      config.Model,
		HTTPClient: defaultHTTPClient(client),
	})
	if err != nil {
		return nil, fmt.Errorf("create eino openai model: %w", err)
	}
	return chatModel, nil
}

// NewEinoAnthropicModel 从 PentGo 的 ProviderConfig 构造一个满足 Eino
// model.ToolCallingChatModel 的 Claude 原生 tool-call 模型。用于 anthropic 路径的
// ADK 单代理循环，与 openai 对称——两个 provider 现在都走 ADK。文本 AnthropicClient
// （anthropic.go）保留，仍供 ConsolidateAndVerify 巩固与报告生成使用。
//
// 不预绑定工具：ADK ChatModelAgent 内部会调 WithTools 注入工具 schema。
func NewEinoAnthropicModel(ctx context.Context, config ProviderConfig, client *http.Client, lookupEnv func(string) (string, bool)) (model.ToolCallingChatModel, error) {
	key, err := apiKey(config, defaultEnvLookup(lookupEnv))
	if err != nil {
		return nil, err
	}
	baseURL, err := einoBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	// claude 的 option.WithBaseURL 由 anthropic SDK 自行拼接 /v1/messages，
	// 故这里传规范化基址即可（与 openai 复用同一 einoBaseURL 校验）。
	endpoint := baseURL
	chatModel, err := einoclaude.NewChatModel(requestContext(ctx), &einoclaude.Config{
		APIKey:     key,
		BaseURL:    &endpoint,
		Model:      config.Model,
		MaxTokens:  einoAnthropicMaxTokens,
		HTTPClient: defaultHTTPClient(client),
	})
	if err != nil {
		return nil, fmt.Errorf("create eino anthropic model: %w", err)
	}
	return chatModel, nil
}

// einoBaseURL 归一化 eino-ext openai 客户端所需的基址。eino-ext 内部会自行拼接
// /chat/completions，故这里只做去尾斜杠与非空校验，不追加路径。
func einoBaseURL(baseURL string) (string, error) {
	return providerEndpoint(baseURL, "")
}
