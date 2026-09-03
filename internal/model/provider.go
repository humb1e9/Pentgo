package model

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	einoclaude "github.com/cloudwego/eino-ext/components/model/claude"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// Provider request limits apply to every model call, including checkpoints.
const (
	anthropicMaxTokens    = 4096
	modelRequestTimeout   = 5 * time.Minute
	modelTransportRetries = 2
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
	client := &http.Client{Timeout: modelRequestTimeout, Transport: retryTransport{base: http.DefaultTransport, attempts: modelTransportRetries}}
	if strings.EqualFold(configuration.Provider, "anthropic") {
		return einoclaude.NewChatModel(ctx, &einoclaude.Config{APIKey: configuration.APIKey, BaseURL: &baseURL, Model: configuration.Model, MaxTokens: anthropicMaxTokens, HTTPClient: client})
	}
	return einoopenai.NewChatModel(ctx, newOpenAIConfig(configuration, baseURL, client))
}

func newOpenAIConfig(configuration Config, baseURL string, client *http.Client) *einoopenai.ChatModelConfig {
	config := &einoopenai.ChatModelConfig{APIKey: configuration.APIKey, BaseURL: baseURL, Model: configuration.Model, HTTPClient: client}
	if effort := configuration.normalizedThinkingEffort(); effort != "" {
		config.ExtraFields = map[string]any{"enable_thinking": true}
		config.ReasoningEffort = thinkingEffort(effort)
	}
	return config
}

func thinkingEffort(value string) einoopenai.ReasoningEffortLevel {
	switch value {
	case "low":
		return einoopenai.ReasoningEffortLevelLow
	case "high":
		return einoopenai.ReasoningEffortLevelHigh
	default:
		return einoopenai.ReasoningEffortLevelMedium
	}
}

// retryTransport retries only pre-response transient connection failures. A
// response is never retried, so a stream that has started cannot be duplicated.
type retryTransport struct {
	base     http.RoundTripper
	attempts int
}

func (transport retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	attempts := max(0, transport.attempts)
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt != 0 {
			if request.GetBody == nil {
				// The original EOF is more useful than replacing it with a
				// synthetic body-replay error.
				return nil, lastErr
			}
			body, err := request.GetBody()
			if err != nil {
				return nil, err
			}
			request = request.Clone(request.Context())
			request.Body = body
		}
		response, err := base.RoundTrip(request)
		lastErr = err
		if response != nil || !retryableTransportError(err) || attempt >= attempts || request.Context().Err() != nil {
			return response, err
		}
		select {
		case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	}
}

func retryableTransportError(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || strings.Contains(strings.ToLower(err.Error()), "tls handshake timeout"))
}
