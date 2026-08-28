package agent

import (
	"strings"

	skillsadapter "pentgo/internal/tools"
)

const maxPreloadedSkills = 3

// matchedSkillContext loads a small, ranked set of relevant skill bodies into
// the current turn. No model-side skill-loading tool or session catalog is used.
func matchedSkillContext(registry *skillsadapter.Registry, request string) string {
	if registry == nil {
		return ""
	}
	var builder strings.Builder
	for _, skill := range registry.Matches(request, maxPreloadedSkills) {
		body, err := registry.Load(skill.Name)
		if err != nil || strings.TrimSpace(body) == "" {
			continue
		}
		builder.WriteString("<pentgo-preloaded-skill name=\"")
		builder.WriteString(skill.Name)
		builder.WriteString("\">\nFollow this relevant skill before specialized work.\n")
		builder.WriteString(body)
		builder.WriteString("\n</pentgo-preloaded-skill>\n")
	}
	return strings.TrimSpace(builder.String())
}
