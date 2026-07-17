package runtime

import (
	"strings"
	"testing"

	"pentgo/skills"
)

func TestBuildSystemPromptListsSkills(t *testing.T) {
	prompt := buildSystemPrompt([]skills.Skill{
		{Name: "recon", Description: "信息收集方法论"},
		{Name: "terminal", Description: "终端通用准则"},
	})
	for _, want := range []string{"SKILL_LOAD", "recon", "信息收集方法论", "terminal", "终端通用准则"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildSystemPromptWithoutSkills(t *testing.T) {
	prompt := buildSystemPrompt(nil)
	if !strings.Contains(prompt, "terminal agent") {
		t.Fatalf("base prompt missing: %s", prompt)
	}
}
