package verify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pentgo/internal/runtime/authz"
)

// vulnerable app: /admin/panel leaks the same admin JSON to both identities.
func newPrivEscServer(t *testing.T, lowPrivBlocked bool) *httptest.Server {
	t.Helper()
	adminBody := `{"id":1,"role":"admin","panel":"secret admin dashboard: all user accounts listed"}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			body, _ := io.ReadAll(r.Body)
			form := string(body)
			switch {
			case strings.Contains(form, "username=lowpriv"):
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-low", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
			case strings.Contains(form, "username=adminuser"):
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-high", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
			default:
				_, _ = io.WriteString(w, "invalid credentials")
			}
		case "/admin/panel":
			cookie := r.Header.Get("Cookie")
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(cookie, "sid=session-high") {
				_, _ = io.WriteString(w, adminBody)
				return
			}
			if strings.Contains(cookie, "sid=session-low") {
				if lowPrivBlocked {
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":"forbidden"}`)
					return
				}
				_, _ = io.WriteString(w, adminBody)
				return
			}
			w.WriteHeader(http.StatusFound)
			w.Header().Set("Location", "/login")
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
}

func TestVerifyWithEvidenceDualSessionPrivEscVulnerable(t *testing.T) {
	server := newPrivEscServer(t, false)
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:   VulnPrivEsc,
		Method:     http.MethodGet,
		URL:        server.URL + "/admin/panel",
		Payload:    "path=/admin/panel",
		LoginURL:   server.URL + "/login",
		LoginBody:  "username=lowpriv&password=x",
		Username:   "lowpriv",
		LoginURLB:  server.URL + "/login",
		LoginBodyB: "username=adminuser&password=y",
		UsernameB:  "adminuser",
	})
	if !record.LoginVerified || !record.LoginBVerified {
		t.Fatalf("both logins must verify: %+v", record)
	}
	if result.Verdict != VerdictVerified && result.Verdict != VerdictLikely {
		t.Fatalf("vulnerable privesc verdict = %+v", result)
	}
	if result.IDORDiffReason == "" {
		t.Fatalf("expected privesc reason: %+v", result)
	}
}

func TestVerifyWithEvidenceDualSessionPrivEscSecureAppBlocksLowPriv(t *testing.T) {
	server := newPrivEscServer(t, true)
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:   VulnPrivEsc,
		Method:     http.MethodGet,
		URL:        server.URL + "/admin/panel",
		Payload:    "path=/admin/panel",
		LoginURL:   server.URL + "/login",
		LoginBody:  "username=lowpriv&password=x",
		Username:   "lowpriv",
		LoginURLB:  server.URL + "/login",
		LoginBodyB: "username=adminuser&password=y",
		UsernameB:  "adminuser",
	})
	if !record.LoginVerified || !record.LoginBVerified {
		t.Fatalf("both logins must verify: %+v", record)
	}
	if result.Verdict == VerdictVerified || result.Verdict == VerdictLikely {
		t.Fatalf("secure app must not verify privesc: %+v", result)
	}
}

func TestVerifyWithEvidenceDualSessionPrivEscSingleSessionInconclusive(t *testing.T) {
	server := newPrivEscServer(t, false)
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:  VulnPrivEsc,
		Method:    http.MethodGet,
		URL:       server.URL + "/admin/panel",
		Payload:   "path=/admin/panel",
		LoginURL:  server.URL + "/login",
		LoginBody: "username=lowpriv&password=x",
		Username:  "lowpriv",
	})
	if record.LoginBVerified {
		t.Fatalf("no session B declared: %+v", record)
	}
	if result.Verdict == VerdictVerified || result.Verdict == VerdictLikely {
		t.Fatalf("single-session privesc must not verify: %+v", result)
	}
}

// Regression (end-to-end HTTP): two verified sessions both fetch the same
// benign page containing only the UI word "Dashboard". This is not a privesc —
// it is shared content both roles are meant to see — and must never verify.
func TestVerifyWithEvidenceDualSessionBenignSharedDashboardNotVerified(t *testing.T) {
	shared := "<html><body><h1>Team Dashboard</h1><nav>Home Reports Settings Logout Profile Help</nav><p>Welcome to your team workspace overview page.</p></body></html>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			body, _ := io.ReadAll(r.Body)
			form := string(body)
			switch {
			case strings.Contains(form, "username=alice"):
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s-alice", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
			case strings.Contains(form, "username=bob"):
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s-bob", Path: "/"})
				_, _ = io.WriteString(w, "dashboard logout")
			default:
				_, _ = io.WriteString(w, "invalid credentials")
			}
		case "/dashboard":
			_, _ = io.WriteString(w, shared)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidence(context.Background(), FindingSpec{
		VulnType:   VulnPrivEsc,
		Method:     http.MethodGet,
		URL:        server.URL + "/dashboard",
		Payload:    "path=/dashboard",
		LoginURL:   server.URL + "/login",
		LoginBody:  "username=alice&password=x",
		Username:   "alice",
		LoginURLB:  server.URL + "/login",
		LoginBodyB: "username=bob&password=y",
		UsernameB:  "bob",
	})
	if !record.LoginVerified || !record.LoginBVerified {
		t.Fatalf("both logins must verify: %+v", record)
	}
	if result.Verdict == VerdictVerified || result.Verdict == VerdictLikely {
		t.Fatalf("benign shared dashboard must not verify privesc: %+v", result)
	}
}

func TestVerifyWithEvidenceOptionsReusesDualSessionPrivEscCookies(t *testing.T) {
	loginPosts := 0
	adminBody := `{"id":1,"role":"admin","panel":"secret admin dashboard: all user accounts listed"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodPost {
				loginPosts++
			}
			w.WriteHeader(http.StatusInternalServerError)
		case "/admin/panel":
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.Header.Get("Cookie"), "sid=low") || strings.Contains(r.Header.Get("Cookie"), "sid=high") {
				_, _ = io.WriteString(w, adminBody)
				return
			}
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Fatalf("path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	verifier := NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	result, record := verifier.VerifyWithEvidenceOptions(context.Background(), FindingSpec{
		VulnType:  VulnPrivEsc,
		Method:    http.MethodGet,
		URL:       server.URL + "/admin/panel",
		LoginURL:  server.URL + "/login",
		LoginURLB: server.URL + "/login",
		Payload:   "path=/admin/panel",
	}, VerifyOptions{
		CookieA:      "sid=low",
		CookieNamesA: []string{"sid"},
		CookieB:      "sid=high",
		CookieNamesB: []string{"sid"},
	})
	if loginPosts != 0 || !record.LoginVerified || !record.LoginBVerified {
		t.Fatalf("login posts/record = %d/%+v", loginPosts, record)
	}
	if result.Verdict != VerdictVerified && result.Verdict != VerdictLikely {
		t.Fatalf("reused-cookie privesc verdict = %+v", result)
	}
}
