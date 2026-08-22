package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pentgo/internal/config"

	"github.com/cloudwego/eino/schema"
)

func TestNewModelPassesConfiguredRequestExtensionsVerbatim(t *testing.T) {
	var request struct {
		Model           string         `json:"model"`
		Thinking        map[string]any `json:"thinking"`
		ReasoningEffort string         `json:"reasoning_effort"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		if incoming.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", incoming.URL.Path)
		}
		if err := json.NewDecoder(incoming.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"fixture","object":"chat.completion","created":0,"model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	chatModel, err := NewModel(context.Background(), config.AgentConfig{
		Provider: "openai",
		OpenAI: config.ModelProviderConfig{
			BaseURL: server.URL + "/v1",
			Model:   "fixture",
			APIKey:  "test-key",
			RequestExtra: map[string]any{
				"thinking":         map[string]any{"type": "enabled"},
				"reasoning_effort": "high",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")}); err != nil {
		t.Fatal(err)
	}
	if request.Model != "fixture" || request.Thinking["type"] != "enabled" || request.ReasoningEffort != "high" {
		t.Fatalf("request = %#v", request)
	}
}

func TestNewModelNormalizesConfiguredResponseReasoningField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"fixture","object":"chat.completion","created":0,"model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok","vendor_reasoning":"inspect before act"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	chatModel, err := NewModel(context.Background(), config.AgentConfig{
		Provider: "openai",
		OpenAI: config.ModelProviderConfig{
			BaseURL:                      server.URL,
			Model:                        "fixture",
			APIKey:                       "test-key",
			ResponseReasoningJSONPointer: "/choices/0/message/vendor_reasoning",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := chatModel.Generate(context.Background(), []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if message.ReasoningContent != "inspect before act" {
		t.Fatalf("reasoning content = %q", message.ReasoningContent)
	}
}

func TestJSONPointerString(t *testing.T) {
	raw := []byte(`{"a/b":{"~key":["skip","value"]}}`)
	if got, ok := jsonPointerString(raw, "/a~1b/~0key/1"); !ok || got != "value" {
		t.Fatalf("value/ok = %q/%v", got, ok)
	}
	if _, ok := jsonPointerString(raw, "a/b"); ok {
		t.Fatal("non-pointer path succeeded")
	}
}
