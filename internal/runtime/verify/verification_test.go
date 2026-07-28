package verify

import (
	"strings"
	"testing"
)

func TestScoreSQLiErrorAgainstBaselineVerified(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnSQLI,
		Payload:           "id=1'",
		ResponseBody:      "You have an error in your SQL syntax near '1''",
		BaselineBody:      "welcome user 1",
		StatusCode:        500,
		BaselineStatus:    200,
		ReproductionCount: 3,
	}
	result := Score(evidence)
	if result.Verdict != VerdictVerified {
		t.Fatalf("verdict = %s, confidence = %.2f, failed = %v", result.Verdict, result.Confidence, result.ChecksFailed)
	}
}

func TestScoreSQLiErrorAlsoInBaselineIsNotVerified(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnSQLI,
		Payload:           "id=1'",
		ResponseBody:      "You have an error in your SQL syntax",
		BaselineBody:      "You have an error in your SQL syntax",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict == VerdictVerified {
		t.Fatalf("baseline error must prevent verification: %+v", result)
	}
}

func TestScoreXSSReflectedVerbatim(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnXSS,
		Payload:           "<script>alert(1)</script>",
		ResponseBody:      "<div><script>alert(1)</script></div>",
		BaselineBody:      "<div>normal</div>",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified {
		t.Fatalf("verbatim reflected XSS should verify: %+v", result)
	}
}

func TestScoreLFIPasswd(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnLFI,
		Payload:           "../../etc/passwd",
		ResponseBody:      "root:x:0:0:root:/root:/bin/bash",
		BaselineBody:      "not found",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified {
		t.Fatalf("LFI passwd signature should verify: %+v", result)
	}
}

func TestScoreRCEIdOutput(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnRCE,
		Payload:           ";id",
		ResponseBody:      "uid=33(www-data) gid=33(www-data)",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified {
		t.Fatalf("RCE id output should verify: %+v", result)
	}
}

func TestScoreAuthBypassStatusTransition(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnAuthBypass,
		Payload:           "role=admin",
		ResponseBody:      "Welcome Admin Dashboard",
		BaselineBody:      "Login required",
		StatusCode:        200,
		BaselineStatus:    302,
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified {
		t.Fatalf("auth bypass transition should verify: %+v", result)
	}
}

func TestScoreUploadSuccessSignature(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnUpload,
		Payload:           "file=test.txt",
		ResponseBody:      `{"success": true, "file": "test.txt"}`,
		BaselineBody:      `{"success": false}`,
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified {
		t.Fatalf("upload signature should verify: %+v", result)
	}
}

func TestScoreOpenRedirectLocationOffsite(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnOpenRedirect,
		TargetHost:        "target.example",
		Payload:           "next=//evil.example",
		LocationHeader:    "https://evil.example/",
		StatusCode:        302,
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict != VerdictVerified && result.Verdict != VerdictLikely {
		t.Fatalf("offsite Location redirect should verify or be likely: %+v", result)
	}
}

func TestScoreCredentialUsesFrameworkLoginVerification(t *testing.T) {
	verified := Score(Evidence{
		VulnType:          VulnCredential,
		Payload:           "username=fixture",
		ResponseBody:      "dashboard",
		LoginVerified:     true,
		ReproductionCount: 1,
	})
	if verified.Verdict != VerdictLikely {
		t.Fatalf("verified credential = %+v", verified)
	}
	if failed := Score(Evidence{VulnType: VulnCredential, ReproductionCount: 1}); failed.Verdict != VerdictRefuted {
		t.Fatalf("failed credential = %+v", failed)
	}
}

func TestScoreNoEvidenceRefuted(t *testing.T) {
	evidence := Evidence{
		VulnType:     VulnSQLI,
		Payload:      "id=1'",
		ResponseBody: "welcome",
		BaselineBody: "welcome",
	}
	if result := Score(evidence); result.Verdict != VerdictRefuted {
		t.Fatalf("no evidence should be refuted: %+v", result)
	}
}

func TestScoreReproductionAndCausalDifferenceCannotVerifyWithoutSignature(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnSQLI,
		Payload:           "id=1'",
		ResponseBody:      "AAA different",
		BaselineBody:      "BBB",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict == VerdictVerified {
		t.Fatalf("missing deterministic signature must prevent verification: %+v", result)
	}
}

func TestScoreUnknownTypeCannotVerify(t *testing.T) {
	evidence := Evidence{
		VulnType:          VulnType("unknown"),
		Payload:           "x=1",
		ResponseBody:      "payload changed response",
		BaselineBody:      "baseline response",
		ReproductionCount: 3,
	}
	if result := Score(evidence); result.Verdict == VerdictVerified {
		t.Fatalf("unknown type must not verify: %+v", result)
	}
}

func TestResponseDiffersBingoStyle(t *testing.T) {
	if ok, reason := ResponseDiffers("", "x"); ok || reason != "empty" {
		t.Fatalf("empty: %v %s", ok, reason)
	}
	if ok, reason := ResponseDiffers("same-body-same-body-same-body-same", "short"); ok {
		t.Fatalf("too short should not differ: %v %s", ok, reason)
	}
	a := `{"id":1,"username":"alice","email":"a@example.test","profile":"owner data here enough length"}`
	b := `{"id":2,"username":"bob","email":"b@example.test","profile":"other user private profile data"}`
	ok, reason := ResponseDiffers(a, b)
	if !ok {
		t.Fatalf("json identity fields should differ: %s", reason)
	}
	if ok, _ := ResponseDiffers(a, a); ok {
		t.Fatal("identical JSON must not differ")
	}
	longA := strings.Repeat("owner-private-content-", 10)
	longB := strings.Repeat("other-user-secret-data-", 12)
	if ok, reason := ResponseDiffers(longA, longB); !ok {
		t.Fatalf("content length diff should match: %s", reason)
	}
}

func TestScoreIDORDualSessionSharedIdentityLeak(t *testing.T) {
	owner := `{"id":2,"username":"bob","email":"bob@example.test","note":"owner profile content"}`
	other := `{"id":2,"username":"bob","email":"bob@example.test","note":"attacker read victim profile content"}`
	result := Score(Evidence{
		VulnType:          VulnIDOR,
		Payload:           "user=2",
		ResponseBody:      other,
		BaselineBody:      owner,
		StatusCode:        200,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: true,
	})
	if result.Verdict != VerdictVerified {
		t.Fatalf("dual-session identity leak should verify: %+v", result)
	}
	if result.IDORDiffReason == "" {
		t.Fatalf("expected idor diff reason: %+v", result)
	}
}

func TestScoreIDORDualSessionNoSharedIdentityIsNotVerified(t *testing.T) {
	owner := `{"id":1,"username":"alice","email":"alice@example.test","note":"owner profile content"}`
	other := `{"id":2,"username":"bob","email":"bob@example.test","note":"other user profile content"}`
	result := Score(Evidence{
		VulnType:          VulnIDOR,
		Payload:           "user=2",
		ResponseBody:      other,
		BaselineBody:      owner,
		StatusCode:        200,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: true,
	})
	if result.Verdict == VerdictVerified {
		t.Fatalf("different owners must not verify IDOR: %+v", result)
	}
}

func TestScoreIDORSingleSessionCannotVerify(t *testing.T) {
	result := Score(Evidence{
		VulnType:          VulnIDOR,
		Payload:           "user=2",
		ResponseBody:      `{"id":2,"username":"bob","email":"bob@example.test","note":"attacker response"}`,
		BaselineBody:      `{"id":1,"username":"alice","email":"alice@example.test","note":"anonymous response"}`,
		StatusCode:        200,
		BaselineStatus:    200,
		ReproductionCount: 3,
		LoginVerified:     true,
	})
	if result.Verdict == VerdictVerified || result.Confidence >= 0.75 {
		t.Fatalf("single-session IDOR must be capped below verified: %+v", result)
	}
}

func TestScoreIDORNoDiffIsNotVerified(t *testing.T) {
	body := strings.Repeat("same-profile-content-", 8)
	result := Score(Evidence{
		VulnType:          VulnIDOR,
		ResponseBody:      body,
		BaselineBody:      body,
		StatusCode:        200,
		ReproductionCount: 3,
		LoginVerified:     true,
		DualLoginVerified: true,
	})
	if result.Verdict == VerdictVerified {
		t.Fatalf("identical responses must not verify idor: %+v", result)
	}
}
