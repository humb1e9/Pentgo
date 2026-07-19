package authz

import (
	"strings"
	"testing"

	"pentgo/internal/runtime/exec"
)


func TestAuthorizerBlocksDestructiveSQL(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://example.com/?q=1 UNION SELECT 1; DROP TABLE users')\n"
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguagePython, Code: code}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "destructive") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerAllowsReadOnlySQL(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://example.com/?id=1 UNION SELECT username FROM users')\n"
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguagePython, Code: code}, scope)
	if !decision.Allowed {
		t.Fatalf("read-only SELECT should be allowed: %+v", decision)
	}
}

func TestAuthorizerBlocksDestructiveShell(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguageShell, Code: "rm -rf /tmp/loot"}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "destructive") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerBlocksOutOfScopeHost(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://evil.com/steal')\n"
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguagePython, Code: code}, scope)
	if decision.Allowed || !strings.Contains(strings.ToLower(decision.Reason), "scope") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAuthorizerAllowsInScopeHost(t *testing.T) {
	authz := NewAuthorizer(false)
	scope := NewScope("example.com", nil, true)
	code := "import requests\nrequests.get('https://api.example.com/status')\n"
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguagePython, Code: code}, scope)
	if !decision.Allowed {
		t.Fatalf("in-scope host should be allowed: %+v", decision)
	}
}

func TestAuthorizerAllowsDestructiveWhenConfigured(t *testing.T) {
	authz := NewAuthorizer(true)
	scope := NewScope("example.com", nil, true)
	decision := authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguageShell, Code: "rm -rf /tmp/loot"}, scope)
	if !decision.Allowed {
		t.Fatalf("destructive should be allowed when configured: %+v", decision)
	}
}

func TestNilAuthorizerAllows(t *testing.T) {
	var authz *Authorizer
	if !authz.Authorize(exec.CodeBlock{Index: 1, Language: exec.LanguageShell, Code: "rm -rf /"}, NewScope("example.com", nil, false)).Allowed {
		t.Fatal("nil authorizer must allow")
	}
}

func TestExtractHostsFromCode(t *testing.T) {
	hosts := extractHosts("a='https://example.com/x'\nb=\"http://evil.com:8080/y\"\nc='not a url'")
	got := strings.Join(hosts, ",")
	if !strings.Contains(got, "example.com") || !strings.Contains(got, "evil.com") {
		t.Fatalf("hosts = %v", hosts)
	}
}
