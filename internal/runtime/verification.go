package runtime

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
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
	VulnType          VulnType
	Payload           string
	ResponseBody      string
	BaselineBody      string
	LocationHeader    string
	TargetHost        string
	StatusCode        int
	BaselineStatus    int
	ReproductionCount int
	LoginVerified     bool
}

// VerificationResult is the deterministic result sent to the report pipeline.
type VerificationResult struct {
	Verdict      Verdict  `json:"verdict"`
	VulnType     VulnType `json:"vuln_type"`
	Confidence   float64  `json:"confidence"`
	ChecksPassed []string `json:"checks_passed,omitempty"`
	ChecksFailed []string `json:"checks_failed,omitempty"`
	Summary      string   `json:"summary"`
	Curl         string   `json:"curl,omitempty"`
	EvidencePath string   `json:"evidence_path,omitempty"`
}

// Score applies bingo-style deterministic, reproducibility, causal, narrow-
// question, and independent-verification weights to framework-captured data.
func Score(evidence Evidence) VerificationResult {
	result := VerificationResult{VulnType: evidence.VulnType}
	confidence := 0.0

	if matched, detail := deterministicCheck(evidence); matched {
		confidence += 0.4
		result.ChecksPassed = append(result.ChecksPassed, "P5 deterministic: "+detail)
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
		if matchHost(strings.ToLower(location.Hostname()), targetHost) {
			return false, "redirect remains in target scope"
		}
		return true, "offsite redirect location"
	default:
		return false, "unsupported vulnerability type"
	}
}
