package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClientChatsWithPlainTextMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("request = %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["tools"]; ok {
			t.Fatalf("request contains tools: %s", body["tools"])
		}
		if _, ok := body["tool_choice"]; ok {
			t.Fatalf("request contains tool_choice: %s", body["tool_choice"])
		}
		var messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body["messages"], &messages); err != nil {
			t.Fatal(err)
		}
		want := []struct{ role, content string }{{"system", "system"}, {"user", "task"}, {"assistant", "decision"}, {"user", "execution result"}}
		if len(messages) != len(want) {
			t.Fatalf("messages = %+v", messages)
		}
		for index, message := range messages {
			if message.Role != want[index].role || message.Content != want[index].content {
				t.Fatalf("message[%d] = %+v, want %+v", index, message, want[index])
			}
		}
		_, _ = io.WriteString(w, "{\"choices\":[{\"message\":{\"content\":\"```python\\nprint('evidence')\\n```\"}}]}")
	}))
	defer server.Close()

	client := NewOpenAIClient(ProviderConfig{BaseURL: server.URL + "/v1", Model: "fixture", APIKeyEnv: "TEST_KEY"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	response, err := client.Chat(context.Background(), Request{SystemPrompt: "system", Messages: []Message{{Role: "user", Content: "task"}, {Role: "assistant", Content: "decision"}, {Role: "user", Content: "execution result"}}})
	if err != nil || response.Content != "```python\nprint('evidence')\n```" {
		t.Fatalf("response/err = %+v/%v", response, err)
	}
}

func TestOpenAIClientIncludesConfiguredThinkingMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Thinking.Type != "disabled" {
			t.Fatalf("thinking = %+v", body.Thinking)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(ProviderConfig{BaseURL: server.URL, Model: "fixture", APIKeyEnv: "TEST_KEY", ThinkingMode: "disabled"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	response, err := client.Chat(context.Background(), Request{SystemPrompt: "system", Messages: []Message{{Role: "user", Content: "task"}}})
	if err != nil || response.Content != "done" {
		t.Fatalf("response/err = %+v/%v", response, err)
	}
}

func TestAnthropicClientChatsWithoutNativeTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Fatalf("request = %s %q %q", r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version"))
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["tools"]; ok {
			t.Fatalf("request contains tools: %s", body["tools"])
		}
		_, _ = io.WriteString(w, "{\"content\":[{\"type\":\"text\",\"text\":\"run this\"},{\"type\":\"thinking\",\"thinking\":\"hidden\"},{\"type\":\"text\",\"text\":\"```bash\\necho evidence\\n```\"}]}")
	}))
	defer server.Close()

	client := NewAnthropicClient(ProviderConfig{BaseURL: server.URL, Model: "fixture", APIKeyEnv: "TEST_KEY"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	response, err := client.Chat(context.Background(), Request{SystemPrompt: "system", Messages: []Message{{Role: "user", Content: "task"}}})
	if err != nil || response.Content != "run this\n```bash\necho evidence\n```" {
		t.Fatalf("response/err = %+v/%v", response, err)
	}
}

func TestClientRejectsMissingAPIKey(t *testing.T) {
	client := NewOpenAIClient(ProviderConfig{BaseURL: "https://example.test/v1", Model: "fixture", APIKeyEnv: "TEST_KEY"}, http.DefaultClient, func(string) (string, bool) { return "", false })
	_, err := client.Chat(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("error = %v", err)
	}
}

func TestAnthropicClientRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer server.Close()

	client := NewAnthropicClient(ProviderConfig{BaseURL: server.URL, Model: "fixture", APIKeyEnv: "TEST_KEY"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	_, err := client.Chat(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v", err)
	}
}
