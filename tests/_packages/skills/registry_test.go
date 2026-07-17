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

func TestCatalogListsSkillsWithDescriptions(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 2 {
		t.Fatalf("catalog length = %d", len(catalog))
	}
	if catalog[0].Name != "recon" || catalog[1].Name != "terminal" {
		t.Fatalf("catalog order = %+v", catalog)
	}
	for _, skill := range catalog {
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
	}
}
