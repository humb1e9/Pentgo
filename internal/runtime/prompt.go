package runtime

import (
	"strings"

	"pentgo/skills"
)

const baseSystemPrompt = `You are PentGo's terminal agent. Work from actual execution output only.

When an operation is needed, return one or more fenced Python or Bash code blocks. Every block must print useful evidence. The runtime executes all supported blocks and returns stdout, stderr, exit status, and evidence references in the next turn.

Use SKILL_LOAD: skill-name on its own line to request a registered local skill. Skills are read-only context, not native tools. Return TASK_COMPLETE or MISSION_COMPLETE only after the required evidence has been returned and do not include code in a completion response.

Only target the authorized host and its subdomains. Do not contact unrelated external hosts and do not perform destructive write operations (INSERT/UPDATE/DELETE/DROP or destructive shell commands); such blocks are rejected before execution.`

// buildSystemPrompt 在基础提示词后追加可加载 Skill 清单，供模型发现并按需 SKILL_LOAD。
func buildSystemPrompt(catalog []skills.Skill) string {
	if len(catalog) == 0 {
		return baseSystemPrompt
	}
	var builder strings.Builder
	builder.WriteString(baseSystemPrompt)
	builder.WriteString("\n\nAvailable skills (load with SKILL_LOAD: <name> when relevant):\n")
	for _, skill := range catalog {
		builder.WriteString("- ")
		builder.WriteString(skill.Name)
		builder.WriteString(": ")
		builder.WriteString(skill.Description)
		builder.WriteString("\n")
	}
	return builder.String()
}
