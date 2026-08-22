package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"pentgo/internal/config"

	einoclaude "github.com/cloudwego/eino-ext/components/model/claude"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// anthropicMaxTokens 提供 Claude 适配器要求的显式输出上限，
// 请求超时仍由共享 HTTP 客户端控制。
const anthropicMaxTokens = 4096

// NewModel 为适配器构造配置指定的原生工具调用模型。
// 它不发起请求，并将凭据保留在领域状态之外。
func NewModel(ctx context.Context, configuration config.AgentConfig) (model.ToolCallingChatModel, error) {
	providerName := strings.TrimSpace(configuration.Provider)
	var provider config.ModelProviderConfig
	switch providerName {
	case "openai":
		provider = configuration.OpenAI
	case "anthropic":
		provider = configuration.Anthropic
	default:
		return nil, fmt.Errorf("unsupported agent provider: %s", providerName)
	}
	if strings.TrimSpace(provider.Model) == "" {
		return nil, fmt.Errorf("agent %s model is empty", providerName)
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return nil, fmt.Errorf("agent %s base URL is empty", providerName)
	}
	if legacyThinking := strings.ToLower(strings.TrimSpace(provider.ThinkingMode)); legacyThinking != "" && legacyThinking != "disabled" {
		return nil, fmt.Errorf("agent %s thinking_mode is deprecated; configure provider-specific request_extra instead", providerName)
	}
	key, err := providerKey(provider)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(configuration.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Minute
	}
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	client := &http.Client{Timeout: timeout}
	if providerName == "anthropic" {
		return einoclaude.NewChatModel(ctxOrBackground(ctx), &einoclaude.Config{APIKey: key, BaseURL: &baseURL, Model: provider.Model, MaxTokens: anthropicMaxTokens, HTTPClient: client})
	}
	chatModel, err := einoopenai.NewChatModel(ctxOrBackground(ctx), &einoopenai.ChatModelConfig{
		APIKey:      key,
		BaseURL:     baseURL,
		Model:       provider.Model,
		HTTPClient:  client,
		ExtraFields: provider.RequestExtra,
	})
	if err != nil {
		return nil, err
	}
	return newConfiguredChatModel(chatModel, provider.ResponseReasoningJSONPointer, provider.StreamResponseReasoningJSONPointer), nil
}

// providerKey 优先使用显式密钥，否则读取配置的环境变量，避免将凭据写入项目持久化数据。
func providerKey(provider config.ModelProviderConfig) (string, error) {
	if key := strings.TrimSpace(provider.APIKey); key != "" {
		return key, nil
	}
	envName := strings.TrimSpace(provider.APIKeyEnv)
	if envName == "" {
		return "", fmt.Errorf("API key environment variable is empty")
	}
	key, ok := os.LookupEnv(envName)
	if !ok || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("API key is not configured in %s", envName)
	}
	return key, nil
}

// ctxOrBackground 为要求非 nil ctx 的 Provider 构造函数提供有效上下文。
func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
