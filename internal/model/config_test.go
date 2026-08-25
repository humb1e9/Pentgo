package model

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConfigRejectsBlankAPIKey(t *testing.T) {
	_, err := New(context.Background(), Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt", APIKey: ""})
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestModelRequestTimeoutAllowsSlowStreamingResponses(t *testing.T) {
	if modelRequestTimeout != 5*time.Minute {
		t.Fatalf("model request timeout = %s, want 5m", modelRequestTimeout)
	}
}
