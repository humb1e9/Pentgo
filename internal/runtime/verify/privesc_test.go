package verify

import (
	"strings"
	"testing"
)

func TestPrivilegedContentLeakedJSONSharedIdentity(t *testing.T) {
	high := `{"id":7,"role":"admin","panel":"secret admin dashboard data for the org"}`
	low := `{"id":7,"role":"admin","panel":"secret admin dashboard data for the org"}`
	leaked, reason := PrivilegedContentLeaked(200, low, high)
	if !leaked || !strings.HasPrefix(reason, "shared_privileged_field_") {
		t.Fatalf("leaked/reason = %v/%q", leaked, reason)
	}
}

// Regression: a benign SHARED page every logged-in user legitimately sees,
// whose HTML merely contains the ubiquitous word "Dashboard", must not clear
// the privileged-baseline gate. Before the privilegedContentSignature split
// this scored VERIFIED confidence=1.00 because adminSignature matched bare
// "dashboard" and the identical bodies satisfied sharedPrivilegedToken.
func TestPrivilegedContentLeakedBenignSharedDashboardNotPrivileged(t *testing.T) {
	page := "<html><body><h1>Team Dashboard</h1><nav>Home Reports Settings Logout Profile Help</nav><p>Welcome to your team workspace overview page.</p></body></html>"
	if leaked, reason := PrivilegedContentLeaked(200, page, page); leaked || reason != "baseline_not_privileged" {
		t.Fatalf("benign shared dashboard: leaked/reason = %v/%q", leaked, reason)
	}
}

func TestPrivilegedContentLeakedBlockedLowPrivStatus(t *testing.T) {
	high := `{"id":7,"role":"admin","panel":"secret admin dashboard data"}`
	for _, status := range []int{403, 302, 401, 500} {
		if leaked, reason := PrivilegedContentLeaked(status, "irrelevant body content here", high); leaked || reason != "low_priv_blocked" {
			t.Fatalf("status %d: leaked/reason = %v/%q", status, leaked, reason)
		}
	}
}

func TestPrivilegedContentLeakedBaselineNotPrivileged(t *testing.T) {
	high := `{"id":7,"title":"public article body that is quite long and generic"}`
	low := `{"id":7,"title":"public article body that is quite long and generic"}`
	if leaked, reason := PrivilegedContentLeaked(200, low, high); leaked || reason != "baseline_not_privileged" {
		t.Fatalf("leaked/reason = %v/%q", leaked, reason)
	}
}

func TestPrivilegedContentLeakedShortBodies(t *testing.T) {
	if leaked, reason := PrivilegedContentLeaked(200, "admin", "admin"); leaked || reason != "insufficient_body" {
		t.Fatalf("leaked/reason = %v/%q", leaked, reason)
	}
}

func TestPrivilegedContentLeakedNonJSONSharedAdminSnippet(t *testing.T) {
	high := "<html><body><h1>Admin Dashboard</h1><table>all user accounts and roles listed here for administration</table></body></html>"
	low := "<html><body><h1>Admin Dashboard</h1><table>all user accounts and roles listed here for administration</table></body></html>"
	leaked, reason := PrivilegedContentLeaked(200, low, high)
	if !leaked || reason != "shared_privileged_content" {
		t.Fatalf("leaked/reason = %v/%q", leaked, reason)
	}
}

func TestPrivilegedContentLeakedLengthMismatchRejected(t *testing.T) {
	// anti-bingo regression: full admin dashboard vs a tiny page that merely
	// contains the word "admin" once must NOT score.
	high := "<html><body><h1>Admin Dashboard</h1>" + strings.Repeat("privileged user record row; ", 80) + "</body></html>"
	low := "<html>you are not an admin here</html>"
	if leaked, reason := PrivilegedContentLeaked(200, low, high); leaked {
		t.Fatalf("length mismatch must not leak: reason=%q", reason)
	}
}

func TestPrivilegedContentLeakedDifferentJSONIdentityNoOverlap(t *testing.T) {
	high := `{"id":1,"role":"admin","note":"the administrative account private profile"}`
	low := `{"id":99,"role":"user","note":"an ordinary unrelated account profile body"}`
	if leaked, reason := PrivilegedContentLeaked(200, low, high); leaked || reason != "no_shared_privileged_field" {
		t.Fatalf("leaked/reason = %v/%q", leaked, reason)
	}
}

func TestScorePrivEscVerifiedDualSessionLeak(t *testing.T) {
	body := `{"id":7,"role":"admin","panel":"secret admin dashboard data for the org"}`
	result := Score(Evidence{
		VulnType:          VulnPrivEsc,
		Payload:           "path=/admin/users",
		ResponseBody:      body,
		BaselineBody:      body,
		StatusCode:        200,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: true,
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("dual-session privesc should verify: %+v", result)
	}
	if result.IDORDiffReason == "" {
		t.Fatalf("expected privesc diff reason: %+v", result)
	}
}

func TestScorePrivEscSingleSessionNeverVerifies(t *testing.T) {
	body := `{"id":7,"role":"admin","panel":"secret admin dashboard data for the org"}`
	result := Score(Evidence{
		VulnType:          VulnPrivEsc,
		Payload:           "path=/admin/users",
		ResponseBody:      body,
		BaselineBody:      body,
		StatusCode:        200,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: false,
	})
	if result.Verdict == VerdictVerified || result.Verdict == VerdictLikely {
		t.Fatalf("single-session privesc must not verify or be likely: %+v", result)
	}
}

func TestScorePrivEscBlockedLowPrivRefutes(t *testing.T) {
	high := `{"id":7,"role":"admin","panel":"secret admin dashboard data for the org"}`
	result := Score(Evidence{
		VulnType:          VulnPrivEsc,
		Payload:           "path=/admin/users",
		ResponseBody:      "Forbidden",
		BaselineBody:      high,
		StatusCode:        403,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: true,
	})
	if result.Verdict == VerdictVerified || result.Verdict == VerdictLikely {
		t.Fatalf("blocked low-priv must not verify: %+v", result)
	}
}

func TestScoreIDORStillUsesCausalDifferenceP2(t *testing.T) {
	// regression: the privesc P2 branch must not alter IDOR's causal-diff wording.
	result := Score(Evidence{
		VulnType:          VulnIDOR,
		ResponseBody:      `{"id":2,"username":"bob","note":"private victim profile content here"}`,
		BaselineBody:      `{"id":1,"username":"alice","note":"private owner profile content here"}`,
		StatusCode:        200,
		ReproductionCount: 3,
		DualLoginVerified: true,
	})
	found := false
	for _, check := range result.ChecksPassed {
		if check == "P2 causal difference" {
			found = true
		}
	}
	if !found {
		t.Fatalf("idor must retain P2 causal difference: %+v", result.ChecksPassed)
	}
}
