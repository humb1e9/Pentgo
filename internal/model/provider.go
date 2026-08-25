package model

import (
	"context"
	"net/http"
	"strings"
	"time"

	einoclaude "github.com/cloudwego/eino-ext/components/model/claude"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// Provider request limits apply to every model call, including checkpoints.
const (
	anthropicMaxTokens  = 4096
	modelRequestTimeout = 5 * time.Minute
)

// NewModel 为适配器构造配置指定的原生工具调用模型。
// 它不发起请求，并将凭据保留在领域状态之外。
// New constructs the configured provider without reading environment variables.
func New(ctx context.Context, configuration Config) (model.ToolCallingChatModel, error) {
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL := strings.TrimRight(strings.TrimSpace(configuration.BaseURL), "/")
	client := &http.Client{Timeout: modelRequestTimeout}
	if strings.EqualFold(configuration.Provider, "anthropic") {
		return einoclaude.NewChatModel(ctx, &einoclaude.Config{APIKey: configuration.APIKey, BaseURL: &baseURL, Model: configuration.Model, MaxTokens: anthropicMaxTokens, HTTPClient: client})
	}
	return einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{APIKey: configuration.APIKey, BaseURL: baseURL, Model: configuration.Model, HTTPClient: client})
}
