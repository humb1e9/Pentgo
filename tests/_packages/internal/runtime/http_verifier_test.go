package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPVerifierConfirmsReflectedXSS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := request.URL.Query().Get("q")
		if value != "" {
			_, _ = io.WriteString(writer, "<div>"+value+"</div>")
			return
		}
		_, _ = io.WriteString(writer, "<div>home</div>")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType:    VulnXSS,
		Method:      http.MethodGet,
		URL:         server.URL + "/?q=<script>alert(1)</script>",
		BaselineURL: server.URL + "/?q=benign",
		Payload:     "<script>alert(1)</script>",
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("verdict = %s failed = %v", result.Verdict, result.ChecksFailed)
	}
	if !strings.Contains(result.Curl, "curl") {
		t.Fatalf("curl = %q", result.Curl)
	}
}

func TestHTTPVerifierRejectsOutOfScope(t *testing.T) {
	verifier := NewHTTPVerifier(http.DefaultClient, NewScope("target.example", nil, false), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType: VulnXSS,
		Method:   http.MethodGet,
		URL:      "https://evil.example/?q=x",
		Payload:  "x",
	})
	if result.Verdict != VerdictInconclusive {
		t.Fatalf("out-of-scope verdict = %s", result.Verdict)
	}
	if len(result.ChecksFailed) != 1 || !strings.Contains(result.ChecksFailed[0], "scope") {
		t.Fatalf("checks failed = %v", result.ChecksFailed)
	}
}

func TestHTTPVerifierRejectsOutOfScopeNonIdempotentBaselineWithoutPayloadRequest(t *testing.T) {
	payloadHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payloadHits++
		_, _ = io.WriteString(writer, `{"success": true}`)
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType:    VulnUpload,
		Method:      http.MethodPost,
		URL:         server.URL + "/upload",
		BaselineURL: "https://evil.example/upload",
		Body:        "file=payload.txt",
		Payload:     "file=payload.txt",
	})
	if result.Verdict != VerdictInconclusive || payloadHits != 0 {
		t.Fatalf("result/payload hits = %+v/%d", result, payloadHits)
	}
}

func TestHTTPVerifierNonIdempotentSingleRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			hits++
		}
		_, _ = io.WriteString(writer, `{"success": true}`)
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	verifier.Verify(context.Background(), FindingSpec{
		VulnType:     VulnUpload,
		Method:       http.MethodPost,
		URL:          server.URL + "/upload",
		BaselineURL:  server.URL + "/upload?mode=baseline",
		Body:         "file=test.txt",
		BaselineBody: "file=benign.txt",
		Payload:      "file=test.txt",
	})
	if hits != 1 {
		t.Fatalf("POST request count = %d, want 1", hits)
	}
}

func TestHTTPVerifierCapturesRedirectWithoutFollowing(t *testing.T) {
	payloadHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/payload" {
			payloadHits++
			writer.Header().Set("Location", "https://evil.example/")
			writer.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(writer, "home")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType:    VulnOpenRedirect,
		Method:      http.MethodGet,
		URL:         server.URL + "/payload",
		BaselineURL: server.URL + "/baseline",
		Payload:     "next=https://evil.example/",
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("redirect verdict = %s, failed = %v", result.Verdict, result.ChecksFailed)
	}
	if payloadHits != 3 {
		t.Fatalf("payload hits = %d, want 3", payloadHits)
	}
}

func TestHTTPVerifierTransportFailureIsInconclusive(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport unavailable")
	})}
	verifier := NewHTTPVerifier(client, NewScope("example.test", nil, false), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType: VulnXSS,
		Method:   http.MethodGet,
		URL:      "https://example.test/?q=x",
		Payload:  "x",
	})
	if result.Verdict != VerdictInconclusive {
		t.Fatalf("transport failure verdict = %s", result.Verdict)
	}
}

func TestHTTPVerifierRejectsUnsupportedMethodWithoutRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		_, _ = io.WriteString(writer, "unexpected")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType: VulnXSS,
		Method:   "GET; touch /tmp/pentgo",
		URL:      server.URL + "/?q=payload",
		Payload:  "payload",
	})
	if result.Verdict != VerdictInconclusive || hits != 0 || result.Curl != "" {
		t.Fatalf("result/hits = %+v/%d", result, hits)
	}
}

func TestCurlCommandIncludesHeadersAndBody(t *testing.T) {
	command := CurlCommand(FindingSpec{
		Method:  http.MethodPost,
		URL:     "https://example.test/upload",
		Body:    "name=O'Reilly",
		Headers: map[string]string{"X-Test": "value"},
	})
	for _, want := range []string{"curl -i -X POST", "-H 'X-Test: value'", "--data-raw 'name=O'\"'\"'Reilly'", "'https://example.test/upload'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("curl command missing %q: %q", want, command)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
