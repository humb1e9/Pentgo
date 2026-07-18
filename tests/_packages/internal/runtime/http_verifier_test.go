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

func TestVerifyWithEvidenceCapturesRawBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := request.URL.Query().Get("q")
		_, _ = io.WriteString(writer, "<div>"+value+"</div>")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:    VulnXSS,
		Method:      http.MethodGet,
		URL:         server.URL + "/?q=<script>alert(1)</script>",
		BaselineURL: server.URL + "/?q=benign",
		Payload:     "<script>alert(1)</script>",
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("verdict = %s", result.Verdict)
	}
	if !strings.Contains(record.PayloadResponseBody, "<script>alert(1)</script>") {
		t.Fatalf("payload response = %q", record.PayloadResponseBody)
	}
	if !strings.Contains(record.BaselineResponseBody, "benign") {
		t.Fatalf("baseline response = %q", record.BaselineResponseBody)
	}
	if record.PayloadStatus != http.StatusOK || record.Reproductions != 3 {
		t.Fatalf("status/reproductions = %d/%d", record.PayloadStatus, record.Reproductions)
	}
}

func TestVerifyWithEvidenceScopeRejectedRecord(t *testing.T) {
	verifier := NewHTTPVerifier(http.DefaultClient, NewScope("target.example", nil, false), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:     VulnXSS,
		Method:       http.MethodGet,
		URL:          "https://evil.example/?q=x",
		Body:         "payload-body",
		BaselineBody: "baseline-body",
		Payload:      "x",
		Headers:      map[string]string{"X-Test": "value"},
	})
	if result.Verdict != VerdictInconclusive || !record.ScopeRejected {
		t.Fatalf("result/scope rejected = %s/%v", result.Verdict, record.ScopeRejected)
	}
	if record.PayloadResponseBody != "" || record.BaselineResponseBody != "" || record.RequestBody != "" || record.BaselineRequestBody != "" || len(record.RequestHeaders) != 0 {
		t.Fatalf("scope-rejected record captured request or response data: %+v", record)
	}
}

func TestVerifyWithEvidenceCapsRecordedRequestBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(writer, "ok")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	verifier.maxBodyBytes = 8
	_, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:     VulnXSS,
		Method:       http.MethodGet,
		URL:          server.URL + "/?q=payload",
		BaselineURL:  server.URL + "/?q=baseline",
		Body:         "payload-body",
		BaselineBody: "baseline-body",
		Payload:      "payload",
	})
	if record.RequestBody != "payload-" || record.BaselineRequestBody != "baseline" {
		t.Fatalf("recorded bodies = %q/%q", record.RequestBody, record.BaselineRequestBody)
	}
}

func TestVerifyStillReturnsResultOnly(t *testing.T) {
	verifier := NewHTTPVerifier(http.DefaultClient, NewScope("target.example", nil, false), 3)
	result := verifier.Verify(context.Background(), FindingSpec{
		VulnType: VulnXSS,
		Method:   http.MethodGet,
		URL:      "https://evil.example/",
		Payload:  "x",
	})
	if result.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %s", result.Verdict)
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

func TestVerifyLoginEstablishesMeaningfulCookieSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/login" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "username=admin&password=admin" {
			t.Fatalf("body/error = %q/%v", body, err)
		}
		http.SetCookie(writer, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
		_, _ = io.WriteString(writer, "dashboard")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{
		LoginURL:  server.URL + "/login",
		LoginBody: "username=admin&password=admin",
	})
	if !outcome.Attempted || !outcome.Verified || !outcome.MeaningfulCookie || outcome.StatusCode != http.StatusOK {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.Contains(outcome.SessionCookieHeader, "sid=abc") || len(outcome.CookieNames) != 1 || outcome.CookieNames[0] != "sid" {
		t.Fatalf("session cookies = %+v", outcome)
	}
}

func TestVerifyLoginRejectsFailureTextAndGenericCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: "PHPSESSID", Value: "generic", Path: "/"})
		_, _ = io.WriteString(writer, "invalid credentials")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{LoginURL: server.URL + "/login"})
	if outcome.Verified || outcome.MeaningfulCookie || !outcome.FailText {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestVerifyLoginDoesNotTreatGenericCookieAsSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: "PHPSESSID", Value: "generic", Path: "/"})
		_, _ = io.WriteString(writer, "sign in")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{LoginURL: server.URL + "/login"})
	if outcome.Verified || outcome.MeaningfulCookie || outcome.SuccessText {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestVerifyLoginRecognizesRedirectAwayFromLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/login" {
			writer.Header().Set("Location", "/dashboard")
			writer.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(writer, "ok")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope(hostOf(server.URL), nil, true), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{LoginURL: server.URL + "/login"})
	if !outcome.RedirectAway || !outcome.Verified {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestVerifyLoginRejectsOutOfScopeBeforeRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		_, _ = io.WriteString(writer, "unexpected")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), NewScope("target.example", nil, false), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{LoginURL: server.URL + "/login"})
	if !outcome.Attempted || outcome.Error == "" || hits != 0 {
		t.Fatalf("outcome/hits = %+v/%d", outcome, hits)
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
