package verify

import (
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
