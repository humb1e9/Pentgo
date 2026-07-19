package verify

import (
	"net/url"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pentgo/internal/runtime/authz"
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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
	verifier := NewHTTPVerifier(http.DefaultClient, authz.NewScope("target.example", nil, false), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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
	verifier := NewHTTPVerifier(http.DefaultClient, authz.NewScope("target.example", nil, false), 3)
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
	verifier := NewHTTPVerifier(http.DefaultClient, authz.NewScope("target.example", nil, false), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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
	verifier := NewHTTPVerifier(client, authz.NewScope("example.test", nil, false), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
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

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope("target.example", nil, false), 3)
	outcome := verifier.verifyLogin(context.Background(), FindingSpec{LoginURL: server.URL + "/login"})
	if !outcome.Attempted || outcome.Error == "" || hits != 0 {
		t.Fatalf("outcome/hits = %+v/%d", outcome, hits)
	}
}

func TestVerifyWithEvidenceScoresCredentialLoginOnce(t *testing.T) {
	loginHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/login" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		loginHits++
		http.SetCookie(writer, &http.Cookie{Name: "sid", Value: "fixture", Path: "/"})
		_, _ = io.WriteString(writer, "dashboard")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:  VulnCredential,
		LoginURL:  server.URL + "/login",
		LoginBody: "username=fixture&password=fixture",
		Username:  "fixture",
	})
	if result.Verdict != VerdictLikely || loginHits != 1 {
		t.Fatalf("result/login hits = %+v/%d", result, loginHits)
	}
	if !record.LoginAttempted || !record.LoginVerified || record.LoginStatus != http.StatusOK || record.Reproductions != 1 {
		t.Fatalf("record = %+v", record)
	}
	if strings.Contains(result.Curl, "password=fixture") || !strings.Contains(result.Curl, "password=REDACTED") {
		t.Fatalf("credential curl = %q", result.Curl)
	}
}

func TestVerifyWithEvidenceRefutesFailedCredentialLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/login" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		_, _ = io.WriteString(writer, "invalid credentials")
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:  VulnCredential,
		LoginURL:  server.URL + "/login",
		LoginBody: "username=fixture&password=wrong",
		Username:  "fixture",
	})
	if result.Verdict != VerdictInconclusive && result.Verdict != VerdictRefuted {
		t.Fatalf("result = %+v", result)
	}
	if !record.LoginAttempted || record.LoginVerified {
		t.Fatalf("record = %+v", record)
	}
}

func TestVerifyWithEvidenceUsesSessionCookieOnlyForPayload(t *testing.T) {
	anonymousHits := 0
	authenticatedPayloadHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/login":
			http.SetCookie(writer, &http.Cookie{Name: "sid", Value: "fixture", Path: "/"})
			_, _ = io.WriteString(writer, "dashboard")
		case "/user/2":
			if strings.Contains(request.Header.Get("Cookie"), "sid=fixture") {
				authenticatedPayloadHits++
				_, _ = io.WriteString(writer, "Welcome Admin Dashboard")
				return
			}
			anonymousHits++
			writer.Header().Set("Location", "/login")
			writer.WriteHeader(http.StatusFound)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:    VulnAuthBypass,
		Method:      http.MethodGet,
		URL:         server.URL + "/user/2",
		BaselineURL: server.URL + "/user/2",
		Payload:     "user=2",
		LoginURL:    server.URL + "/login",
		LoginBody:   "username=fixture&password=fixture",
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("result = %+v", result)
	}
	if !record.LoginVerified || authenticatedPayloadHits != 3 || anonymousHits != 1 {
		t.Fatalf("record/authenticated/anonymous = %+v/%d/%d", record, authenticatedPayloadHits, anonymousHits)
	}
}

func TestVerifyWithEvidenceLoginFailureDoesNotUpgradeAnonymousFinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/login":
			_, _ = io.WriteString(writer, "invalid credentials")
		case "/feature":
			_, _ = io.WriteString(writer, "login required")
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:  VulnXSS,
		Method:    http.MethodGet,
		URL:       server.URL + "/feature?q=payload",
		Payload:   "payload",
		LoginURL:  server.URL + "/login",
		LoginBody: "username=fixture&password=fixture",
	})
	if result.Verdict == VerdictVerified || !strings.Contains(strings.Join(result.ChecksFailed, "\n"), "auth session not established") {
		t.Fatalf("result = %+v", result)
	}
	if !record.LoginAttempted || record.LoginVerified {
		t.Fatalf("record = %+v", record)
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

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Hostname()
}

func TestVerifyWithEvidenceDualSessionIDOR(t *testing.T) {
	// bingo two-user mode: A accesses B's object; B baseline is owner view of same URL? 
	// Our semantics: payload Cookie=A hits /user/2 (B's object); baseline Cookie=B hits /user/2.
	// Server returns different JSON per cookie identity.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			body, _ := io.ReadAll(r.Body)
			form := string(body)
			if strings.Contains(form, "username=userA") {
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-a", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
				return
			}
			if strings.Contains(form, "username=userB") {
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-b", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
				return
			}
			_, _ = io.WriteString(w, "invalid credentials")
		case "/user/2":
			cookie := r.Header.Get("Cookie")
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(cookie, "sid=session-a") {
				// A reading B's object (IDOR)
				_, _ = io.WriteString(w, `{"id":2,"username":"userB","email":"b@example.test","secret":"victim-private-data-here"}`)
				return
			}
			if strings.Contains(cookie, "sid=session-b") {
				// B reading own object
				_, _ = io.WriteString(w, `{"id":2,"username":"userB","email":"b@example.test","secret":"owner-view-of-self-data"}`)
				return
			}
			w.WriteHeader(http.StatusFound)
			w.Header().Set("Location", "/login")
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:    VulnIDOR,
		Method:      http.MethodGet,
		URL:         server.URL + "/user/2",
		Payload:     "user=2",
		LoginURL:    server.URL + "/login",
		LoginBody:   "username=userA&password=a",
		Username:    "userA",
		LoginURLB:   server.URL + "/login",
		LoginBodyB:  "username=userB&password=b",
		UsernameB:   "userB",
	})
	if !record.LoginVerified || !record.LoginBVerified {
		t.Fatalf("both logins must verify: %+v", record)
	}
	if result.Verdict != VerdictVerified && result.Verdict != VerdictLikely {
		t.Fatalf("dual-session idor verdict = %+v", result)
	}
	if !strings.Contains(record.PayloadResponseBody, "victim-private-data") {
		t.Fatalf("payload should be A viewing B: %q", record.PayloadResponseBody)
	}
	if !strings.Contains(record.BaselineResponseBody, "owner-view") {
		t.Fatalf("baseline should be B own view: %q", record.BaselineResponseBody)
	}
	if result.UsernameB != "userB" || !result.LoginBVerified {
		t.Fatalf("result login B metadata = %+v", result)
	}
}

func TestVerifyWithEvidenceIDORNoDiffRefutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "same", Path: "/"})
			_, _ = io.WriteString(w, "dashboard logout")
		case "/user/2":
			_, _ = io.WriteString(w, strings.Repeat("identical-profile-body-", 6))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, _ := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:   VulnIDOR,
		Method:     http.MethodGet,
		URL:        server.URL + "/user/2",
		LoginURL:   server.URL + "/login",
		LoginBody:  "username=userA&password=a",
		LoginURLB:  server.URL + "/login",
		LoginBodyB: "username=userB&password=b",
	})
	if result.Verdict == VerdictVerified {
		t.Fatalf("identical dual-session responses must not verify: %+v", result)
	}
}
