package model

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRetryTransportRetriesReplayablePreResponseFailures(t *testing.T) {
	for _, failure := range []error{io.EOF, errors.New("net/http: TLS handshake timeout")} {
		t.Run(failure.Error(), func(t *testing.T) {
			calls := 0
			transport := retryTransport{attempts: 2, base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, failure
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
			})}
			request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions", bytes.NewBufferString("body"))
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil || response == nil || calls != 2 {
				t.Fatalf("response/error/calls = %#v/%v/%d", response, err, calls)
			}
			_ = response.Body.Close()
		})
	}
}

func TestRetryTransportDoesNotRetryResponseOrUnreplayableBody(t *testing.T) {
	calls := 0
	response := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("bad gateway"))}
	transport := retryTransport{attempts: 2, base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response, io.EOF
	})}
	request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions", io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := transport.RoundTrip(request)
	if got != response || !errors.Is(err, io.EOF) || calls != 1 {
		t.Fatalf("response/error/calls = %#v/%v/%d", got, err, calls)
	}
}

func TestModelRequestTimeoutAllowsSlowStreamingResponses(t *testing.T) {
	if modelRequestTimeout != 5*time.Minute {
		t.Fatalf("model request timeout = %s, want 5m", modelRequestTimeout)
	}
}
