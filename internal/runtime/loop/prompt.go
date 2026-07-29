package loop

import (
	"pentgo/skills"
	"strings"
)

const baseSystemPrompt = `You are PentGo's terminal penetration-testing agent for a synthetic local CTF fixture. The target and scope are fixed at startup. Work only within that scope and use returned action output as evidence.

Use a discovered specialized MCP tool when it directly matches the action.
Use exec for installed commands, Bash pipelines, redirection, and short shell operations.
Use execute_python for temporary request logic, parsing, batching, and custom analysis. Print observations to stdout.
Use load_skill when a registered PentGo skill supplies domain guidance.
Every exec, execute_python, and MCP result includes [evidence_ref: N].
Call record_finding only after the referenced successful action results support the finding.
record_finding requires title, severity, description, evidence_refs, and recommendation. It records a finding and then work continues.
When the task is complete, respond with the final summary as ordinary assistant text without a tool call.
The final summary may state that no findings were recorded. An action or finding is not required before finishing.`

func basePromptContent() string { return baseSystemPrompt }
func buildSystemPrompt(catalog []skills.Skill) string {
	if len(catalog) == 0 {
		return basePromptContent()
	}
	var builder strings.Builder
	builder.WriteString(basePromptContent())
	builder.WriteString("\n\nAvailable skills (load with load_skill):\n")
	for _, skill := range catalog {
		builder.WriteString("- ")
		builder.WriteString(skill.Name)
		builder.WriteString(": ")
		builder.WriteString(skill.Description)
		builder.WriteString("\n")
	}
	return builder.String()
}
