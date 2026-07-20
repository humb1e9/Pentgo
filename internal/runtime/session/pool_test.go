package session

import (
	"strings"
	"testing"
	"time"
)

func TestSessionPoolPutGetVerifiedOnly(t *testing.T) {
	pool := NewSessionPool()
	pool.Put(&AuthSession{
		Name:         "user_a",
		Role:         "user",
		Username:     "alice",
		CookieHeader: "sid=abc",
		CookieNames:  []string{"sid"},
		Verified:     true,
	})
	pool.Put(&AuthSession{
		Name:         "user_b",
		CookieHeader: "sid=xyz",
		CookieNames:  []string{"sid"},
		Verified:     false,
	})
	got, ok := pool.Get("user_a")
	if !ok || got.CookieHeader != "sid=abc" || got.Username != "alice" {
		t.Fatalf("Get user_a = %+v ok=%v", got, ok)
	}
	if _, ok := pool.Get("user_b"); ok {
		t.Fatal("unverified session must not be Get-able")
	}
	if _, ok := pool.Get("user-a"); ok {
		t.Fatal("invalid name must not match")
	}
}

func TestSessionPoolExportCookieEnv(t *testing.T) {
	pool := NewSessionPool()
	pool.Put(&AuthSession{
		Name:         "user_a",
		Role:         "user",
		Username:     "alice",
		CookieHeader: "sid=secret-value",
		CookieNames:  []string{"sid"},
		Verified:     true,
	})
	env := pool.ExportCookieEnv()
	if env["PENTGO_SESSIONS"] != "user_a" {
		t.Fatalf("PENTGO_SESSIONS = %q", env["PENTGO_SESSIONS"])
	}
	if env["PENTGO_SESSION_user_a_COOKIE"] != "sid=secret-value" {
		t.Fatalf("cookie env = %q", env["PENTGO_SESSION_user_a_COOKIE"])
	}
	if env["PENTGO_SESSION_user_a_USER"] != "alice" || env["PENTGO_SESSION_user_a_ROLE"] != "user" {
		t.Fatalf("meta env = %+v", env)
	}
}

func TestSessionPoolPublicViewHasNoCookieValues(t *testing.T) {
	pool := NewSessionPool()
	pool.Put(&AuthSession{
		Name:             "user_a",
		Username:         "alice",
		LoginURL:         "https://t.example/login",
		CookieHeader:     "sid=secret-value",
		CookieNames:      []string{"sid"},
		MeaningfulCookie: true,
		Verified:         true,
		LoginStatus:      200,
		EstablishedAt:    time.Unix(1, 0).UTC(),
	})
	view := pool.PublicView()
	if len(view) != 1 {
		t.Fatalf("view = %+v", view)
	}
	if view[0].Name != "user_a" || !view[0].Verified || len(view[0].CookieNames) != 1 || view[0].CookieNames[0] != "sid" {
		t.Fatalf("public = %+v", view[0])
	}
	encoded := view[0].Name + view[0].Username + view[0].LoginURL + strings.Join(view[0].CookieNames, ",")
	if strings.Contains(encoded, "secret-value") {
		t.Fatal("public view leaked cookie value")
	}
}

func TestSessionPoolGetByRoleAndNames(t *testing.T) {
	pool := NewSessionPool()
	pool.Put(&AuthSession{Name: "admin1", Role: "admin", CookieHeader: "a=1", Verified: true})
	pool.Put(&AuthSession{Name: "user1", Role: "user", CookieHeader: "u=1", Verified: true})
	got, ok := pool.GetByRole("admin")
	if !ok || got.Name != "admin1" {
		t.Fatalf("GetByRole = %+v ok=%v", got, ok)
	}
	names := pool.Names()
	if len(names) != 2 || names[0] != "admin1" || names[1] != "user1" {
		t.Fatalf("Names = %v", names)
	}
}

func TestSessionNameFromIdentity(t *testing.T) {
	if got := SessionNameFromIdentity("user_a", "alice", "default"); got != "user_a" {
		t.Fatalf("explicit = %q", got)
	}
	if got := SessionNameFromIdentity("", "alice", "default"); got != "alice" {
		t.Fatalf("username = %q", got)
	}
	if got := SessionNameFromIdentity("", "alice!", "default"); got != "default" {
		t.Fatalf("fallback = %q", got)
	}
	if got := NormalizeSessionName("bad-name"); got != "" {
		t.Fatalf("invalid normalize = %q", got)
	}
}

func TestSessionPoolPutIgnoresInvalidName(t *testing.T) {
	pool := NewSessionPool()
	pool.Put(&AuthSession{Name: "bad-name", CookieHeader: "x=1", Verified: true})
	if len(pool.Names()) != 0 {
		t.Fatalf("names = %v", pool.Names())
	}
}
