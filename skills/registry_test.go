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
	if len(names) != 35 {
		t.Fatalf("len(names) = %d, want 35", len(names))
	}
	index := make(map[string]bool, len(names))
	for _, n := range names {
		index[n] = true
	}
	for _, must := range []string{"recon", "terminal", "waf-bypass", "sqli-sql-injection", "ssrf-server-side-request-forgery", "http-403-bypass", "race-condition"} {
		if !index[must] {
			t.Fatalf("names missing %q: %q", must, names)
		}
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("names not sorted ascending at %d: %q", i, names)
		}
	}
}

func TestCatalogListsSkillsWithDescriptions(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 35 {
		t.Fatalf("catalog length = %d, want 35", len(catalog))
	}
	for _, skill := range catalog {
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
	}
}

func TestLoadMigratedSkill(t *testing.T) {
	for _, name := range []string{"waf-bypass", "nosql-injection", "type-juggling", "sqli-sql-injection", "xxe-xml-external-entity", "http-403-bypass", "deserialization-insecure"} {
		content, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q) error = %v", name, err)
		}
		if strings.TrimSpace(content) == "" {
			t.Fatalf("Load(%q) returned empty", name)
		}
	}
}
