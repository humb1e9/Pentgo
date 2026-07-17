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
	want := []string{"nosql-injection", "recon", "terminal", "type-juggling", "waf-bypass"}
	if len(names) != len(want) {
		t.Fatalf("names = %q", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %q, want %q", names, want)
		}
	}
}

func TestCatalogListsSkillsWithDescriptions(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 5 {
		t.Fatalf("catalog length = %d", len(catalog))
	}
	if catalog[0].Name != "nosql-injection" {
		t.Fatalf("catalog order = %+v", catalog)
	}
	for _, skill := range catalog {
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
	}
}

func TestLoadMigratedSkill(t *testing.T) {
	for _, name := range []string{"waf-bypass", "nosql-injection", "type-juggling"} {
		content, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q) error = %v", name, err)
		}
		if strings.TrimSpace(content) == "" {
			t.Fatalf("Load(%q) returned empty", name)
		}
	}
}
