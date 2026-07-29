package report

import (
	"fmt"
	"strings"
	"time"

	sess "pentgo/internal/runtime/session"
)

func renderMarkdown(session *sess.AgentSession) string {
	var builder strings.Builder
	builder.WriteString("# PentGo Report\n\n## Task\n\n")
	fmt.Fprintf(&builder, "- Engagement ID: `%s`\n", inline(session.ID))
	fmt.Fprintf(&builder, "- Target: `%s`\n", inline(session.Target))
	fmt.Fprintf(&builder, "- Intent: `%s`\n", inline(session.Intent))
	fmt.Fprintf(&builder, "- Status: `%s`\n", session.Status)
	fmt.Fprintf(&builder, "- Stop reason: `%s`\n", inline(session.StopReason))
	fmt.Fprintf(&builder, "- Turns: `%d`\n", session.Turns)
	fmt.Fprintf(&builder, "- Started: `%s`\n", session.StartedAt.Format(time.RFC3339))
	if session.FinishedAt != nil {
		fmt.Fprintf(&builder, "- Finished: `%s`\n", session.FinishedAt.Format(time.RFC3339))
	}
	builder.WriteString("\n## Findings\n\n")
	if len(session.Findings) == 0 {
		builder.WriteString("No findings were recorded.\n")
	} else {
		builder.WriteString("The following findings were recorded by the Agent.\n\n")
		for _, finding := range session.Findings {
			fmt.Fprintf(&builder, "### [%s] %s\n\n%s\n\n", strings.ToUpper(finding.Severity), finding.Title, finding.Description)
			refs := make([]string, len(finding.EvidenceRefs))
			for index, ref := range finding.EvidenceRefs {
				refs[index] = fmt.Sprintf("`#%d`", ref)
			}
			fmt.Fprintf(&builder, "Evidence: %s\n\nRecommendation: %s\n\n", strings.Join(refs, ", "), finding.Recommendation)
		}
	}
	builder.WriteString("\n## Agent Summary\n\n")
	if strings.TrimSpace(session.FinalSummary) == "" {
		builder.WriteString("No final summary was produced.\n")
	} else {
		builder.WriteString(strings.TrimSpace(session.FinalSummary) + "\n")
	}
	return builder.String()
}
func inline(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
