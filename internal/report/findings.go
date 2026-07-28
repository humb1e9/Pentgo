package report

import (
	"fmt"
	"strings"

	"pentgo/internal/runtime/verify"
)

// RenderVerifiedFindings renders framework verdicts without model involvement.
func RenderVerifiedFindings(findings []verify.VerificationResult) string {
	verified := make([]verify.VerificationResult, 0, len(findings))
	likely := make([]verify.VerificationResult, 0, len(findings))
	unverified := make([]verify.VerificationResult, 0, len(findings))
	for _, finding := range findings {
		switch finding.Verdict {
		case verify.VerdictVerified:
			verified = append(verified, finding)
		case verify.VerdictLikely:
			likely = append(likely, finding)
		default:
			unverified = append(unverified, finding)
		}
	}

	var builder strings.Builder
	builder.WriteString("## 已验证发现\n\n")
	if len(findings) == 0 {
		builder.WriteString("框架未验证漏洞。\n")
		return builder.String()
	}
	renderFindingGroup(&builder, "确认漏洞", verified)
	renderFindingGroup(&builder, "疑似发现", likely)
	renderFindingGroup(&builder, "声明未获框架验证", unverified)
	return builder.String()
}

func renderFindingGroup(builder *strings.Builder, heading string, findings []verify.VerificationResult) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(builder, "### %s\n\n", heading)
	for index, finding := range findings {
		fmt.Fprintf(builder, "#### %d. %s\n\n", index+1, strings.ToUpper(string(finding.VulnType)))
		fmt.Fprintf(builder, "- Verdict: `%s`\n", finding.Verdict)
		fmt.Fprintf(builder, "- Confidence: `%.2f`\n", finding.Confidence)
		if finding.Summary != "" {
			fmt.Fprintf(builder, "- Summary: %s\n", inline(finding.Summary))
		}
		if finding.Curl != "" {
			fmt.Fprintf(builder, "- Curl: `%s`\n", inline(finding.Curl))
		}
		if finding.EvidencePath != "" {
			fmt.Fprintf(builder, "- Framework evidence: `%s`\n", inline(finding.EvidencePath))
		}
		if len(finding.ChecksPassed) > 0 {
			fmt.Fprintf(builder, "- Checks passed: %s\n", inline(strings.Join(finding.ChecksPassed, "; ")))
		}
		if len(finding.ChecksFailed) > 0 {
			fmt.Fprintf(builder, "- Checks failed: %s\n", inline(strings.Join(finding.ChecksFailed, "; ")))
		}
		if finding.VulnType == verify.VulnCredential || finding.VulnType == verify.VulnIDOR || finding.VulnType == verify.VulnPrivEsc {
			fmt.Fprintf(builder, "- Login verified: %t\n", finding.LoginVerified)
			if len(finding.LoginCookieNames) > 0 {
				fmt.Fprintf(builder, "- Session cookies: %s\n", inline(strings.Join(finding.LoginCookieNames, ", ")))
			}
			if finding.Username != "" {
				fmt.Fprintf(builder, "- Username: %s\n", inline(finding.Username))
			}
		}
		if finding.VulnType == verify.VulnIDOR || finding.VulnType == verify.VulnPrivEsc {
			fmt.Fprintf(builder, "- Login B verified: %t\n", finding.LoginBVerified)
			if len(finding.LoginBCookieNames) > 0 {
				fmt.Fprintf(builder, "- Session B cookies: %s\n", inline(strings.Join(finding.LoginBCookieNames, ", ")))
			}
			if finding.UsernameB != "" {
				fmt.Fprintf(builder, "- Username B: %s\n", inline(finding.UsernameB))
			}
			if finding.IDORDiffReason != "" {
				label := "IDOR diff"
				if finding.VulnType == verify.VulnPrivEsc {
					label = "Privilege escalation"
				}
				fmt.Fprintf(builder, "- %s: %s\n", label, inline(finding.IDORDiffReason))
			}
		}
		builder.WriteString("\n")
	}
}
