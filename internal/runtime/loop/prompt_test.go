package loop

import (
	"pentgo/skills"
	"strings"
	"testing"
)

func TestBuildSystemPromptListsSkills(t *testing.T) {
	prompt := buildSystemPrompt([]skills.Skill{{Name: "recon", Description: "fixture guidance"}})
	if !strings.Contains(prompt, "recon") || !strings.Contains(prompt, "fixture guidance") {
		t.Fatalf("prompt = %s", prompt)
	}
}
