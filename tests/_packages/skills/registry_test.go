package skills

import (
	"strings"
	"testing"
)

func TestLoadReturnsRegisteredReadOnlySkill(t *testing.T) {
	prompt, err := Load("terminal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "SKILL_LOAD") || !strings.Contains(prompt, "代码块") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestLoadRejectsUnknownAndTraversalNames(t *testing.T) {
	for _, name := range []string{"unknown", "../recon", "recon/SKILL.md"} {
		if _, err := Load(name); err == nil {
			t.Fatalf("Load(%q) error = nil", name)
		}
	}
}

func TestNamesListsRegisteredSkills(t *testing.T) {
	names := Names()
	if len(names) != 2 || names[0] != "recon" || names[1] != "terminal" {
		t.Fatalf("names = %q", names)
	}
}
