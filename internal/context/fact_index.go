package context

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf8"

	projectmodel "pentgo/internal/project"
)

// ProjectFactIndexMaxRunes is the fixed upper bound for one turn's
// model-visible project-fact snapshot.
const ProjectFactIndexMaxRunes = 4096

// RenderProjectFactIndex formats key-sorted, untrusted facts for a provider
// system envelope. It includes only complete fact lines within the fixed rune
// cap; remaining facts are reported as omitted.
func RenderProjectFactIndex(facts []projectmodel.ProjectFact) string {
	ordered := append([]projectmodel.ProjectFact(nil), facts...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Key < ordered[right].Key
	})
	lines := make([]string, 0, len(ordered))
	for _, fact := range ordered {
		candidate := append(lines, renderProjectFactLine(fact))
		if utf8.RuneCountInString(renderProjectFactIndex(candidate, len(ordered)-len(candidate))) > ProjectFactIndexMaxRunes {
			break
		}
		lines = candidate
	}
	// Dropping the first over-limit line can add a digit to omitted. Recheck the
	// final envelope so metadata changes cannot push it past the fixed cap.
	for len(lines) != 0 && utf8.RuneCountInString(renderProjectFactIndex(lines, len(ordered)-len(lines))) > ProjectFactIndexMaxRunes {
		lines = lines[:len(lines)-1]
	}
	return renderProjectFactIndex(lines, len(ordered)-len(lines))
}

func renderProjectFactIndex(lines []string, omitted int) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf(`<project-facts shown="%d" omitted="%d">`, len(lines), omitted))
	if len(lines) != 0 {
		builder.WriteByte('\n')
		builder.WriteString(strings.Join(lines, "\n"))
	}
	builder.WriteString("\n\nUse get_project_fact or list_project_facts for details or omitted facts.\n</project-facts>")
	return builder.String()
}

func renderProjectFactLine(fact projectmodel.ProjectFact) string {
	line := "- " + escapeProjectFactText(fact.Key) + ": " + escapeProjectFactText(fact.Value)
	if fact.EvidenceRef != nil {
		line += fmt.Sprintf(" [evidence_ref: %d]", *fact.EvidenceRef)
	}
	return line
}

func escapeProjectFactText(value string) string {
	value = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(value)
	return html.EscapeString(value)
}
