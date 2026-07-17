package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicClient 使用 Messages 文本协议请求 Runtime 响应。
type AnthropicClient struct {
	config    ProviderConfig
	client    *http.Client
	lookupEnv func(string) (string, bool)
}

// NewAnthropicClient 按指定连接配置创建 Anthropic 原生协议客户端。
func NewAnthropicClient(config ProviderConfig, client *http.Client, lookupEnv func(string) (string, bool)) *AnthropicClient {
	return &AnthropicClient{
		config:    config,
		client:    defaultHTTPClient(client),
		lookupEnv: defaultEnvLookup(lookupEnv),
	}
}

// Chat 请求模型生成普通文本。
func (c *AnthropicClient) Chat(ctx context.Context, request Request) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("nil Anthropic client")
	}
	key, err := apiKey(c.config, c.lookupEnv)
	if err != nil {
		return Response{}, err
	}
	endpoint, err := providerEndpoint(c.config.BaseURL, "/v1/messages")
	if err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(anthropicRequest{
		Model:     c.config.Model,
		MaxTokens: 1024,
		System:    request.SystemPrompt,
		Messages:  anthropicMessages(request.Messages),
	})
	if err != nil {
		return Response{}, fmt.Errorf("encode Anthropic request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(requestContext(ctx), http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return Response{}, fmt.Errorf("create Anthropic request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Api-Key", key)
	httpRequest.Header.Set("Anthropic-Version", "2023-06-01")
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("send Anthropic request: %w", err)
	}
	defer response.Body.Close()
	data, err := readResponseBody(response.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read Anthropic response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Response{}, fmt.Errorf("Anthropic API returned HTTP %d", response.StatusCode)
	}
	return parseAnthropicResponse(data)
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func anthropicMessages(messages []Message) []anthropicMessage {
	result := make([]anthropicMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if strings.TrimSpace(message.Content) == "" || (role != "user" && role != "assistant") {
			continue
		}
		result = append(result, anthropicMessage{Role: role, Content: message.Content})
	}
	return result
}

func parseAnthropicResponse(data []byte) (Response, error) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Response{}, fmt.Errorf("decode Anthropic response: %w", err)
	}
	text := make([]string, 0, len(response.Content))
	for _, block := range response.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			text = append(text, strings.TrimSpace(block.Text))
		}
	}
	return Response{Content: strings.Join(text, "\n")}, nil
}
