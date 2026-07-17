package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const maxResponseBytes = 1 << 20

// Client 定义模型在单个 Runtime 回合生成普通文本的边界。
type Client interface {
	Chat(context.Context, Request) (Response, error)
}

// ProviderConfig 保存一个原生模型协议所需的连接信息。
type ProviderConfig struct {
	BaseURL      string
	Model        string
	APIKey       string
	APIKeyEnv    string
	ThinkingMode string
}

// Request 是提交给模型的单回合文本上下文。
type Request struct {
	SystemPrompt string
	Messages     []Message
}

// Message 是提交给模型的一条对话记录。
type Message struct {
	Role    string
	Content string
}

// Response 是模型返回的普通文本。
type Response struct {
	Content string
}

func defaultHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

func defaultEnvLookup(lookup func(string) (string, bool)) func(string) (string, bool) {
	if lookup == nil {
		return os.LookupEnv
	}
	return lookup
}

func apiKey(config ProviderConfig, lookup func(string) (string, bool)) (string, error) {
	if value := strings.TrimSpace(config.APIKey); value != "" {
		return value, nil
	}
	envName := strings.TrimSpace(config.APIKeyEnv)
	if envName == "" {
		return "", fmt.Errorf("API key environment variable is empty")
	}
	value, ok := lookup(envName)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("API key is not configured in %s", envName)
	}
	return value, nil
}

func providerEndpoint(baseURL, path string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is empty")
	}
	return baseURL + path, nil
}

func readResponseBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("model response exceeds %d bytes", maxResponseBytes)
	}
	return data, nil
}

func requestContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
