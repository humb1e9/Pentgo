package session

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// AuthSession is a framework-owned login identity for one engagement.
// Cookie values stay in memory only; PublicView never exposes them.
type AuthSession struct {
	Name             string
	Role             string
	Username         string
	LoginURL         string
	CookieHeader     string
	CookieNames      []string
	MeaningfulCookie bool
	LoginStatus      int
	Verified         bool
	CSRFToken        string
	EstablishedAt    time.Time
}

// SessionPublic is the report/session.json view of an auth session (no secrets).
type SessionPublic struct {
	Name             string    `json:"name"`
	Role             string    `json:"role,omitempty"`
	Username         string    `json:"username,omitempty"`
	LoginURL         string    `json:"login_url,omitempty"`
	CookieNames      []string  `json:"cookie_names,omitempty"`
	MeaningfulCookie bool      `json:"meaningful_cookie,omitempty"`
	Verified         bool      `json:"verified"`
	LoginStatus      int       `json:"login_status,omitempty"`
	EstablishedAt    time.Time `json:"established_at,omitempty"`
}

// SessionPool holds verified (and failed) auth identities for one engagement.
type SessionPool struct {
	mu     sync.Mutex
	byName map[string]*AuthSession
}

// NewSessionPool creates an empty engagement session pool.
func NewSessionPool() *SessionPool {
	return &SessionPool{byName: make(map[string]*AuthSession)}
}

// NormalizeSessionName returns a safe session key or empty if invalid.
func NormalizeSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || !sessionNamePattern.MatchString(name) {
		return ""
	}
	return name
}

// Put stores or replaces a session. Invalid names are ignored.
func (pool *SessionPool) Put(session *AuthSession) {
	if pool == nil || session == nil {
		return
	}
	name := NormalizeSessionName(session.Name)
	if name == "" {
		return
	}
	session.Name = name
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.byName == nil {
		pool.byName = make(map[string]*AuthSession)
	}
	cloned := *session
	cloned.CookieNames = append([]string(nil), session.CookieNames...)
	pool.byName[name] = &cloned
}

// Get returns a verified session by name.
func (pool *SessionPool) Get(name string) (*AuthSession, bool) {
	if pool == nil {
		return nil, false
	}
	name = NormalizeSessionName(name)
	if name == "" {
		return nil, false
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	session, ok := pool.byName[name]
	if !ok || session == nil || !session.Verified || session.CookieHeader == "" {
		return nil, false
	}
	cloned := *session
	cloned.CookieNames = append([]string(nil), session.CookieNames...)
	return &cloned, true
}

// GetByRole returns the first verified session with the given role.
func (pool *SessionPool) GetByRole(role string) (*AuthSession, bool) {
	if pool == nil {
		return nil, false
	}
	role = strings.TrimSpace(strings.ToLower(role))
	pool.mu.Lock()
	defer pool.mu.Unlock()
	names := make([]string, 0, len(pool.byName))
	for name := range pool.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		session := pool.byName[name]
		if session == nil || !session.Verified || session.CookieHeader == "" {
			continue
		}
		if role == "" || strings.EqualFold(session.Role, role) {
			cloned := *session
			cloned.CookieNames = append([]string(nil), session.CookieNames...)
			return &cloned, true
		}
	}
	return nil, false
}

// Names returns verified session names in sorted order.
func (pool *SessionPool) Names() []string {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	names := make([]string, 0, len(pool.byName))
	for name, session := range pool.byName {
		if session != nil && session.Verified && session.CookieHeader != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ExportCookieEnv builds process-local env vars for model code blocks.
// Cookie values appear only here — never in PublicView or evidence JSON.
func (pool *SessionPool) ExportCookieEnv() map[string]string {
	if pool == nil {
		return nil
	}
	names := pool.Names()
	if len(names) == 0 {
		return nil
	}
	env := make(map[string]string, 1+3*len(names))
	env["PENTGO_SESSIONS"] = strings.Join(names, ",")
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, name := range names {
		session := pool.byName[name]
		if session == nil {
			continue
		}
		prefix := "PENTGO_SESSION_" + name
		env[prefix+"_COOKIE"] = session.CookieHeader
		if session.Username != "" {
			env[prefix+"_USER"] = session.Username
		}
		if session.Role != "" {
			env[prefix+"_ROLE"] = session.Role
		}
	}
	return env
}

// PublicView returns desensitized session metadata for session.json / reports.
func (pool *SessionPool) PublicView() []SessionPublic {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	names := make([]string, 0, len(pool.byName))
	for name := range pool.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SessionPublic, 0, len(names))
	for _, name := range names {
		session := pool.byName[name]
		if session == nil {
			continue
		}
		out = append(out, SessionPublic{
			Name:             session.Name,
			Role:             session.Role,
			Username:         session.Username,
			LoginURL:         session.LoginURL,
			CookieNames:      append([]string(nil), session.CookieNames...),
			MeaningfulCookie: session.MeaningfulCookie,
			Verified:         session.Verified,
			LoginStatus:      session.LoginStatus,
			EstablishedAt:    session.EstablishedAt,
		})
	}
	return out
}

// SessionNameFromIdentity picks a stable pool key for a login identity.
func SessionNameFromIdentity(explicit, username, fallback string) string {
	if name := NormalizeSessionName(explicit); name != "" {
		return name
	}
	if name := NormalizeSessionName(username); name != "" {
		return name
	}
	if name := NormalizeSessionName(fallback); name != "" {
		return name
	}
	return ""
}

// FormatSessionSummary is a short timeline/detail string without secrets.
func FormatSessionSummary(session *AuthSession) string {
	if session == nil {
		return "nil"
	}
	return fmt.Sprintf("%s verified=%t cookies=%s", session.Name, session.Verified, strings.Join(session.CookieNames, ","))
}
