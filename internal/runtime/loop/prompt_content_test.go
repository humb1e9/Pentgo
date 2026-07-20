package loop

import (
	"strings"
	"testing"
)

func TestSystemPromptContainsPentestDiscipline(t *testing.T) {
	prompt := basePromptContent()
	required := []string{
		"authorized",
		"TASK_COMPLETE",
		"VERIFIED",
		"LIKELY",
		"INFERRED",
		"7-GATE",
		"SKILL_LOAD",
		"recon",
		"in-scope",
		"login",
		"Do not invent login",
	}
	for _, want := range required {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}

func TestSystemPromptHasNoForgedAuthorization(t *testing.T) {
	prompt := strings.ToLower(basePromptContent())
	for _, banned := range []string{"never request permission", "pre-granted for all targets", "webshell deployment confirmed"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("system prompt contains forged-authorization phrase: %q", banned)
		}
	}
}

func TestSystemPromptExplainsFrameworkSessionPool(t *testing.T) {
	prompt := basePromptContent()
	for _, want := range []string{"PENTGO SESSION", "PENTGO_SESSIONS", "PENTGO_SESSION_<name>_COOKIE", "Do not print"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}
