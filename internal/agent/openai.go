package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenAIClient 使用 Chat Completions 文本协议请求 Runtime 响应。
type OpenAIClient struct {
	config    ProviderConfig
	client    *http.Client
	lookupEnv func(string) (string, bool)
}

// NewOpenAIClient 按指定连接配置创建 OpenAI 原生协议客户端。
func NewOpenAIClient(config ProviderConfig, client *http.Client, lookupEnv func(string) (string, bool)) *OpenAIClient {
	return &OpenAIClient{
		config:    config,
		client:    defaultHTTPClient(client),
		lookupEnv: defaultEnvLookup(lookupEnv),
	}
}

// Chat 请求模型生成普通文本。
func (c *OpenAIClient) Chat(ctx context.Context, request Request) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("nil OpenAI client")
	}
	key, err := apiKey(c.config, c.lookupEnv)
	if err != nil {
		return Response{}, err
	}
	endpoint, err := providerEndpoint(c.config.BaseURL, "/chat/completions")
	if err != nil {
		return Response{}, err
	}
	var thinking *openAIThinking
	if mode := strings.TrimSpace(c.config.ThinkingMode); mode != "" {
		thinking = &openAIThinking{Type: mode}
	}
	body, err := json.Marshal(openAIRequest{
		Model:    c.config.Model,
		Messages: openAIMessages(request),
		Thinking: thinking,
	})
	if err != nil {
		return Response{}, fmt.Errorf("encode OpenAI request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(requestContext(ctx), http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return Response{}, fmt.Errorf("create OpenAI request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+key)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("send OpenAI request: %w", err)
	}
	defer response.Body.Close()
	data, err := readResponseBody(response.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Response{}, fmt.Errorf("OpenAI API returned HTTP %d", response.StatusCode)
	}
	return parseOpenAIResponse(data)
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Thinking *openAIThinking `json:"thinking,omitempty"`
}

type openAIThinking struct {
	Type string `json:"type"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func openAIMessages(request Request) []openAIMessage {
	messages := make([]openAIMessage, 0, len(request.Messages)+1)
	if systemPrompt := request.SystemPrompt; strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: systemPrompt})
	}
	for _, message := range request.Messages {
		role := strings.TrimSpace(message.Role)
		if strings.TrimSpace(message.Content) == "" || (role != "system" && role != "user" && role != "assistant") {
			continue
		}
		messages = append(messages, openAIMessage{Role: role, Content: message.Content})
	}
	return messages
}

func parseOpenAIResponse(data []byte) (Response, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Response{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if len(response.Choices) == 0 {
		return Response{}, fmt.Errorf("OpenAI response has no choices")
	}
	return Response{Content: strings.TrimSpace(response.Choices[0].Message.Content)}, nil
}
