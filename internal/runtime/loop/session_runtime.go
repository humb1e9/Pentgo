package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	sess "pentgo/internal/runtime/session"
)

func (runner *Runner) establishDeclaredSessions(ctx context.Context, specs []SessionSpec, session *sess.AgentSession, turn int) bool {
	if len(specs) == 0 {
		return false
	}
	if runner.sessionPool == nil {
		runner.sessionPool = sess.NewSessionPool()
	}
	establisher, available := runner.config.Verifier.(sessionEstablisher)
	results := make([]string, 0, len(specs))
	for _, spec := range specs {
		if _, ok := runner.sessionPool.Get(spec.Name); ok {
			session.AddEvent(turn, "session_reused", spec.Name, time.Now().UTC())
			results = append(results, "SESSION RESULT: "+spec.Name+" verified (reused)")
			continue
		}
		if !available {
			session.AddEvent(turn, "session_failed", spec.Name+": verifier unavailable", time.Now().UTC())
			results = append(results, "SESSION RESULT: "+spec.Name+" failed (framework verifier unavailable)")
			continue
		}
		outcome := establisher.EstablishSession(ctx, spec.toLoginSpec())
		identity := &sess.AuthSession{
			Name:             spec.Name,
			Role:             spec.Role,
			Username:         spec.Username,
			LoginURL:         spec.LoginURL,
			CookieHeader:     outcome.SessionCookieHeader,
			CookieNames:      append([]string(nil), outcome.CookieNames...),
			MeaningfulCookie: outcome.MeaningfulCookie,
			LoginStatus:      outcome.StatusCode,
			Verified:         outcome.Verified,
			CSRFToken:        outcome.CSRFToken,
			EstablishedAt:    time.Now().UTC(),
		}
		runner.sessionPool.Put(identity)
		if outcome.Verified && identity.CookieHeader != "" {
			session.AddEvent(turn, "session_established", sess.FormatSessionSummary(identity), time.Now().UTC())
			results = append(results, fmt.Sprintf("SESSION RESULT: %s verified; cookie names: %s", spec.Name, strings.Join(identity.CookieNames, ",")))
			continue
		}
		session.AddEvent(turn, "session_failed", spec.Name, time.Now().UTC())
		results = append(results, "SESSION RESULT: "+spec.Name+" failed")
	}
	session.Sessions = runner.sessionPool.PublicView()
	runner.history.Append("user", strings.Join(results, "\n"))
	return true
}
