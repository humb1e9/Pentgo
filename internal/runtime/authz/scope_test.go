package authz

import (
	"testing"
)


func TestScopeAllowsTargetAndSubdomains(t *testing.T) {
	scope := NewScope("example.com", nil, false)
	for _, host := range []string{"example.com", "api.example.com", "a.b.example.com"} {
		if !scope.HostAllowed(host) {
			t.Fatalf("host %q should be in scope", host)
		}
	}
}

func TestScopeBlocksOtherHosts(t *testing.T) {
	scope := NewScope("example.com", nil, false)
	for _, host := range []string{"evil.com", "notexample.com", "example.com.evil.com"} {
		if scope.HostAllowed(host) {
			t.Fatalf("host %q should be out of scope", host)
		}
	}
}

func TestScopeAllowsExtraAllowedHosts(t *testing.T) {
	scope := NewScope("example.com", []string{"cdn.trusted.net"}, false)
	if !scope.HostAllowed("cdn.trusted.net") {
		t.Fatal("explicitly allowed host should be in scope")
	}
	if !scope.HostAllowed("x.cdn.trusted.net") {
		t.Fatal("subdomain of allowed host should be in scope")
	}
}

func TestScopePrivateHostsGatedByFlag(t *testing.T) {
	blocked := NewScope("example.com", nil, false)
	allowed := NewScope("example.com", nil, true)
	for _, host := range []string{"localhost", "127.0.0.1", "10.0.0.5", "192.168.1.1", "::1"} {
		if blocked.HostAllowed(host) {
			t.Fatalf("private host %q must be blocked when allowPrivate=false", host)
		}
		if !allowed.HostAllowed(host) {
			t.Fatalf("private host %q must be allowed when allowPrivate=true", host)
		}
	}
}

func TestScopeAllowsEmptyHost(t *testing.T) {
	if !NewScope("example.com", nil, false).HostAllowed("") {
		t.Fatal("empty host (relative URL) should be allowed")
	}
}
