package verify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"pentgo/internal/runtime/authz"
)

// Verdict is the framework-owned conclusion for a declared finding.
type Verdict string

const (
	VerdictVerified     Verdict = "VERIFIED"
	VerdictLikely       Verdict = "LIKELY"
	VerdictInconclusive Verdict = "INCONCLUSIVE"
	VerdictRefuted      Verdict = "REFUTED"
)

// VulnType identifies the deterministic signature family to apply.
type VulnType string

const (
	VulnSQLI         VulnType = "sqli"
	VulnXSS          VulnType = "xss"
	VulnLFI          VulnType = "lfi"
	VulnRCE          VulnType = "rce"
	VulnAuthBypass   VulnType = "auth_bypass"
	VulnCredential   VulnType = "credential"
	VulnIDOR         VulnType = "idor"
	VulnUpload       VulnType = "upload"
	VulnOpenRedirect VulnType = "open_redirect"
)

var (
	sqliSignature   = regexp.MustCompile(`(?i)sql syntax|mysql_fetch|you have an error|ora-|pg::|sqlite_|\b1064\b`)
	xssSignature    = regexp.MustCompile(`(?i)<script|onerror=|onload=|javascript:`)
	lfiSignature    = regexp.MustCompile(`(?i)root:x:0:0:|\[drivers\]|<\?php|define\s*\(`)
	rceSignature    = regexp.MustCompile(`(?i)uid=\d+\(|volume serial number|nt authority\\system`)
	adminSignature  = regexp.MustCompile(`(?i)\badmin\b|administrator|dashboard`)
	uploadSignature = regexp.MustCompile(`(?i)"success"\s*:\s*true|file.*uploaded|\.(?:php|jsp|asp|aspx)(?:[?/'"]|$)`)
)

// Evidence is the response data captured by a framework-owned verifier.
type Evidence struct {
	VulnType           VulnType
	Payload            string
	ResponseBody       string
	BaselineBody       string
	LocationHeader     string
	TargetHost         string
	StatusCode         int
	BaselineStatus     int
	ReproductionCount  int
	LoginVerified      bool
	DualLoginVerified  bool // bingo two-user mode: both A and B sessions established
	IDORDiffReason     string
}

// VerificationResult is the deterministic result sent to the report pipeline.
type VerificationResult struct {
	Verdict                 Verdict  `json:"verdict"`
	VulnType                VulnType `json:"vuln_type"`
	Confidence              float64  `json:"confidence"`
	ChecksPassed            []string `json:"checks_passed,omitempty"`
	ChecksFailed            []string `json:"checks_failed,omitempty"`
	Summary                 string   `json:"summary"`
	Curl                    string   `json:"curl,omitempty"`
	EvidencePath            string   `json:"evidence_path,omitempty"`
	LoginAttempted          bool     `json:"login_attempted,omitempty"`
	LoginVerified           bool     `json:"login_verified,omitempty"`
	LoginStatus             int      `json:"login_status,omitempty"`
	LoginCookieNames        []string `json:"login_cookie_names,omitempty"`
	LoginMeaningfulCookie   bool     `json:"login_meaningful_cookie,omitempty"`
	Username                string   `json:"username,omitempty"`
	LoginBAttempted         bool     `json:"login_b_attempted,omitempty"`
	LoginBVerified          bool     `json:"login_b_verified,omitempty"`
	LoginBStatus            int      `json:"login_b_status,omitempty"`
	LoginBCookieNames       []string `json:"login_b_cookie_names,omitempty"`
	LoginBMeaningfulCookie  bool     `json:"login_b_meaningful_cookie,omitempty"`
	UsernameB               string   `json:"username_b,omitempty"`
	IDORDiffReason          string   `json:"idor_diff_reason,omitempty"`
}

// Score applies bingo-style deterministic, reproducibility, causal, narrow-
// question, and independent-verification weights to framework-captured data.
func Score(evidence Evidence) VerificationResult {
	result := VerificationResult{VulnType: evidence.VulnType}
	confidence := 0.0

	if matched, detail := deterministicCheck(evidence); matched {
		confidence += 0.4
		result.ChecksPassed = append(result.ChecksPassed, "P5 deterministic: "+detail)
		if evidence.VulnType == VulnIDOR {
			result.IDORDiffReason = detail
		}
	} else {
		result.ChecksFailed = append(result.ChecksFailed, "P5 deterministic: "+detail)
	}

	if evidence.ReproductionCount >= 3 {
		confidence += 0.25
		result.ChecksPassed = append(result.ChecksPassed, "P1 reproducible")
	} else {
		result.ChecksFailed = append(result.ChecksFailed, "P1 reproduction count below 3")
	}

	if evidence.BaselineBody == "" {
		result.ChecksFailed = append(result.ChecksFailed, "P2 no baseline")
	} else if evidence.ResponseBody == "" {
		result.ChecksFailed = append(result.ChecksFailed, "P2 no response body")
	} else if evidence.BaselineBody == evidence.ResponseBody {
		result.ChecksFailed = append(result.ChecksFailed, "P2 no causal difference")
	} else {
		confidence += 0.2
		result.ChecksPassed = append(result.ChecksPassed, "P2 causal difference")
	}

	if strings.Count(evidence.Payload, "&") <= 1 {
		confidence += 0.1
		result.ChecksPassed = append(result.ChecksPassed, "P3 narrow payload")
	} else {
		result.ChecksFailed = append(result.ChecksFailed, "P3 multiple payload variables")
	}

	if evidence.ReproductionCount > 0 {
		confidence += 0.05
		result.ChecksPassed = append(result.ChecksPassed, "P4 independent verification request")
	} else {
		result.ChecksFailed = append(result.ChecksFailed, "P4 no verification request")
	}

	if confidence > 1 {
		confidence = 1
	}
	result.Confidence = confidence
	switch {
	case confidence >= 0.75:
		result.Verdict = VerdictVerified
	case confidence >= 0.45:
		result.Verdict = VerdictLikely
	case confidence >= 0.2:
		result.Verdict = VerdictInconclusive
	default:
		result.Verdict = VerdictRefuted
	}
	result.Summary = fmt.Sprintf("%s %s confidence=%.2f", result.VulnType, result.Verdict, result.Confidence)
	return result
}

func deterministicCheck(evidence Evidence) (bool, string) {
	response := evidence.ResponseBody
	baseline := evidence.BaselineBody
	newMatch := func(pattern *regexp.Regexp) bool {
		return pattern.MatchString(response) && !pattern.MatchString(baseline)
	}

	switch evidence.VulnType {
	case VulnSQLI:
		if newMatch(sqliSignature) {
			return true, "SQL error signature"
		}
		if strings.Contains(strings.ToLower(evidence.Payload), "union") && len(baseline) > 0 && len(response) > len(baseline)*3/2 {
			return true, "UNION response length difference"
		}
		return false, "no SQL signature unique to payload response"
	case VulnXSS:
		if evidence.Payload != "" && strings.Contains(response, evidence.Payload) && !strings.Contains(baseline, evidence.Payload) {
			return true, "payload reflected verbatim"
		}
		if newMatch(xssSignature) {
			return true, "XSS reflection signature"
		}
		return false, "no XSS reflection unique to payload response"
	case VulnLFI:
		if newMatch(lfiSignature) {
			return true, "LFI file-content signature"
		}
		return false, "no LFI signature unique to payload response"
	case VulnRCE:
		if newMatch(rceSignature) {
			return true, "RCE command-output signature"
		}
		return false, "no RCE signature unique to payload response"
	case VulnAuthBypass:
		if newMatch(adminSignature) {
			return true, "admin-only response signature"
		}
		if evidence.BaselineStatus == 302 && evidence.StatusCode == 200 {
			return true, "authentication status transition"
		}
		return false, "no authentication-bypass signature"
	case VulnCredential:
		if evidence.LoginVerified {
			return true, "framework login verified"
		}
		return false, "login not verified"
	case VulnIDOR:
		// bingo tools/idor_scanner.py: dual identity + meaningful response diff
		if evidence.StatusCode != 200 && evidence.StatusCode != 201 && evidence.StatusCode != 206 {
			return false, "idor payload status not accessible"
		}
		differs, reason := ResponseDiffers(evidence.BaselineBody, evidence.ResponseBody)
		if !differs {
			return false, "idor no meaningful response diff: " + reason
		}
		if evidence.DualLoginVerified {
			return true, "dual-session idor diff: " + reason
		}
		if evidence.LoginVerified {
			return true, "authenticated idor diff: " + reason
		}
		return true, "idor response diff: " + reason
	case VulnUpload:
		if newMatch(uploadSignature) {
			return true, "upload success signature"
		}
		return false, "no upload success signature"
	case VulnOpenRedirect:
		location, err := url.Parse(evidence.LocationHeader)
		if err != nil || location.Hostname() == "" {
			return false, "no absolute redirect location"
		}
		targetHost := strings.ToLower(strings.TrimSpace(evidence.TargetHost))
		if targetHost == "" {
			return false, "missing target host"
		}
		if authz.MatchHost(strings.ToLower(location.Hostname()), targetHost) {
			return false, "redirect remains in target scope"
		}
		return true, "offsite redirect location"
	default:
		return false, "unsupported vulnerability type"
	}
}

// ResponseDiffers implements bingo tools/idor_scanner._response_differs:
// two responses differ in a security-meaningful way (not empty/short/same-length noise).
// bodyA is the control (user A or owner); bodyB is the cross-access response (user B or other id).
func ResponseDiffers(bodyA, bodyB string) (bool, string) {
	if bodyA == "" || bodyB == "" {
		return false, "empty"
	}
	if len(bodyB) < 50 {
		return false, "too_short"
	}
	if absInt(len(bodyA)-len(bodyB)) < 20 && bodyA == bodyB {
		return false, "identical"
	}
	if absInt(len(bodyA)-len(bodyB)) < 20 {
		// same rough length — still allow JSON identity-field diffs below
	} else {
		// length-based signal (bingo: same_length rejects <20 delta first)
	}

	// JSON field comparison (bingo: id/user_id/email/username/name)
	if ja, okA := tryJSONMap(bodyA); okA {
		if jb, okB := tryJSONMap(bodyB); okB {
			for _, key := range []string{"id", "user_id", "email", "username", "name", "uid", "userId"} {
				va, hasA := ja[key]
				vb, hasB := jb[key]
				if hasB && (!hasA || fmt.Sprint(va) != fmt.Sprint(vb)) {
					return true, "different_" + key + ": " + fmt.Sprint(vb)
				}
			}
			if bodyA != bodyB {
				return true, "json_differs"
			}
		}
	}

	if bodyA == bodyB {
		return false, "identical"
	}
	ratio := float64(len(bodyB)) / float64(maxInt(len(bodyA), 1))
	if ratio > 0.5 && ratio < 2.0 && len(bodyB) > 100 {
		return true, fmt.Sprintf("content_differs (len:%d)", len(bodyB))
	}
	if absInt(len(bodyA)-len(bodyB)) >= 20 {
		return true, fmt.Sprintf("content_differs (len:%d)", len(bodyB))
	}
	return false, "no_diff"
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func tryJSONMap(body string) (map[string]any, bool) {
	body = strings.TrimSpace(body)
	if body == "" || (body[0] != '{' && body[0] != '[') {
		return nil, false
	}
	// only object maps get identity-field comparison
	if body[0] != '{' {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return nil, false
	}
	return m, true
}
