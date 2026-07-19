package report

import (
	"fmt"
	"strings"
	"time"

	sess "pentgo/internal/runtime/session"
)

func renderMarkdown(session *sess.AgentSession, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# PentGo Agent Report\n\n")
	fmt.Fprintf(&builder, "- Engagement ID: `%s`\n", inline(session.ID))
	fmt.Fprintf(&builder, "- Target: `%s`\n", inline(session.Target.Canonical))
	fmt.Fprintf(&builder, "- Intent: `%s`\n", inline(session.Intent))
	fmt.Fprintf(&builder, "- Status: `%s`\n", session.Status)
	fmt.Fprintf(&builder, "- Stop reason: `%s`\n", inline(session.StopReason))
	fmt.Fprintf(&builder, "- Turns: `%d`\n", session.Turn)
	fmt.Fprintf(&builder, "- Started: `%s`\n", session.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&builder, "- Generated: `%s`\n", generatedAt.Format(time.RFC3339))
	if session.FinishedAt != nil {
		fmt.Fprintf(&builder, "- Finished: `%s`\n", session.FinishedAt.Format(time.RFC3339))
	}
	if len(session.LoadedSkills) > 0 {
		fmt.Fprintf(&builder, "- Loaded skills: `%s`\n", inline(strings.Join(session.LoadedSkills, ", ")))
	}
	builder.WriteString("\n")
	builder.WriteString(RenderVerifiedFindings(session.Findings))
	builder.WriteString("\n## Execution Timeline\n\n")
	if len(session.Timeline) == 0 {
		builder.WriteString("No runtime events recorded.\n")
		return builder.String()
	}
	for _, event := range session.Timeline {
		fmt.Fprintf(&builder, "- `%s` turn `%d` `%s`", event.At.Format(time.RFC3339), event.Turn, inline(event.Kind))
		if event.Detail != "" {
			fmt.Fprintf(&builder, ": %s", inline(event.Detail))
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func inline(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
