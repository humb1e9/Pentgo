package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// ProviderConfig 保存一个原生模型协议所需的连接信息。
type ProviderConfig struct {
	BaseURL      string
	Model        string
	APIKey       string
	APIKeyEnv    string
	ThinkingMode string
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

func requestContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
