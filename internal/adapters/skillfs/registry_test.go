package skillfs

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

func TestRegistryScanSortsDigestsRendersAndLoadsLazily(t *testing.T) {
	source := fstest.MapFS{
		"zeta.md":  &fstest.MapFile{Data: []byte("---\ndescription: Zeta\t skill\n---\nSECRET ZETA BODY\n")},
		"alpha.md": &fstest.MapFile{Data: []byte("---\ndescription:  Alpha\n  skill  \n---\nSECRET ALPHA BODY\n")},
	}
	registry := NewRegistry(source)

	first := registry.Scan()
	second := registry.Scan()
	if first.Digest == "" || len(first.Digest) != 64 {
		t.Fatalf("digest = %q, want nonempty SHA-256 hex", first.Digest)
	}
	if first.Digest != second.Digest || first.Digest != registry.Digest() {
		t.Fatalf("digest is not stable: first=%q second=%q registry=%q", first.Digest, second.Digest, registry.Digest())
	}
	if got, want := first.Catalog, []Skill{{Name: "alpha", Description: "Alpha skill"}, {Name: "zeta", Description: "Zeta skill"}}; !sameCatalog(got, want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	if !registry.HasSkills() {
		t.Fatal("HasSkills() = false, want true")
	}
	if len(first.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want none", first.Diagnostics)
	}

	rendered := registry.RenderCatalog(false)
	if !strings.HasPrefix(rendered, "<pentgo-skill-catalog digest=\""+first.Digest+"\">") {
		t.Fatalf("rendered catalog lacks marker/digest: %q", rendered)
	}
	for _, want := range []string{"`alpha`：Alpha skill", "`zeta`：Zeta skill", "call load_skill with its exact name", "Do not guess skill names"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered catalog %q does not contain %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "SECRET") {
		t.Fatalf("catalog leaked skill body: %q", rendered)
	}
	if !strings.HasSuffix(rendered, "</pentgo-skill-catalog>") {
		t.Fatalf("rendered catalog lacks closing marker: %q", rendered)
	}

	body, err := registry.Load(" alpha ")
	if err != nil {
		t.Fatal(err)
	}
	if body != "SECRET ALPHA BODY" {
		t.Fatalf("Load body = %q", body)
	}
	if strings.Contains(body, "---") {
		t.Fatalf("Load retained frontmatter: %q", body)
	}

	catalog := registry.Catalog()
	catalog[0].Name = "mutated"
	if registry.Catalog()[0].Name != "alpha" {
		t.Fatal("Catalog did not return a defensive copy")
	}
	first.Catalog[0].Name = "mutated result"
	if registry.Catalog()[0].Name != "alpha" {
		t.Fatal("ScanResult did not return a defensive copy")
	}
}

func TestRegistrySkipsInvalidSkillsAndLoadsOnlyValidatedPaths(t *testing.T) {
	source := fstest.MapFS{
		"valid.md":        &fstest.MapFile{Data: []byte("---\ndescription: Valid skill\n---\nvalid body\n")},
		"malformed.md":    &fstest.MapFile{Data: []byte("---\ndescription: [unterminated\n---\nbad body\n")},
		"missing-desc.md": &fstest.MapFile{Data: []byte("---\ntitle: Ignored title\n---\nmissing description body\n")},
		"plain.md":        &fstest.MapFile{Data: []byte("# no frontmatter\nplain body\n")},
	}
	registry := NewRegistry(source)

	result := registry.Scan()
	if got, want := result.Catalog, []Skill{{Name: "valid", Description: "Valid skill"}}; !sameCatalog(got, want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	if len(result.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %#v, want three", result.Diagnostics)
	}
	for _, path := range []string{"malformed.md", "missing-desc.md", "plain.md"} {
		if !hasDiagnostic(result.Diagnostics, path) {
			t.Errorf("missing diagnostic for %s: %#v", path, result.Diagnostics)
		}
	}
	if body, err := registry.Load("valid"); err != nil || body != "valid body" {
		t.Fatalf("Load(valid) = %q, %v", body, err)
	}
	if _, err := registry.Load("malformed"); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("Load(malformed) error = %v, want ErrUnknownSkill", err)
	}
	if _, err := registry.Load("missing-desc"); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("Load(missing-desc) error = %v, want ErrUnknownSkill", err)
	}
	if _, err := registry.Load("plain"); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("Load(plain) error = %v, want ErrUnknownSkill", err)
	}
}

func TestRegistryUnavailableFSReportsDiagnosticAndEmptyReplacement(t *testing.T) {
	registry := NewRegistry(nil)
	result := registry.Scan()
	if len(result.Catalog) != 0 || result.Digest != "" || registry.HasSkills() {
		t.Fatalf("unavailable result = %#v, HasSkills=%v", result, registry.HasSkills())
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Path != "skills" {
		t.Fatalf("diagnostics = %#v, want one skills diagnostic", result.Diagnostics)
	}
	if got, want := registry.RenderCatalog(true), "No PentGo skills are currently available. Do not use names from earlier PentGo skill catalogs."; !strings.Contains(got, want) {
		t.Fatalf("replacement rendering = %q, missing %q", got, want)
	}
	if _, err := registry.Load("anything"); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("Load(anything) error = %v, want ErrUnknownSkill", err)
	}
}

func TestNormalizeDescriptionTruncatesUnicodeAtRuneBoundary(t *testing.T) {
	input := strings.Repeat("  猫\t", 200)
	got := normalizeDescription(input)
	if !utf8.ValidString(got) {
		t.Fatalf("normalized description is invalid UTF-8: %q", got)
	}
	if len(got) > maxCatalogDescriptionBytes {
		t.Fatalf("normalized description length = %d, want <= %d", len(got), maxCatalogDescriptionBytes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated description = %q, want ellipsis", got)
	}
	if strings.Contains(got, "  ") || strings.Contains(got, "\t") || strings.Contains(got, "\n") {
		t.Fatalf("description was not whitespace-normalized: %q", got)
	}

	registry := NewRegistry(fstest.MapFS{
		"unicode.md": &fstest.MapFile{Data: []byte("---\ndescription: " + input + "\n---\nbody\n")},
	})
	result := registry.Scan()
	if len(result.Catalog) != 1 {
		t.Fatalf("catalog = %#v", result.Catalog)
	}
	if result.Catalog[0].Description != got {
		t.Fatalf("catalog description = %q, want %q", result.Catalog[0].Description, got)
	}
	if !strings.Contains(registry.RenderCatalog(false), got) {
		t.Fatal("renderer did not use compact description")
	}
	if invalid := normalizeDescription(string([]byte{'x', 0xff, 'y'})); !utf8.ValidString(invalid) {
		t.Fatalf("invalid input remained invalid UTF-8: %q", invalid)
	}
}

func sameCatalog(got, want []Skill) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func hasDiagnostic(diagnostics []Diagnostic, path string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && diagnostic.Reason != "" {
			return true
		}
	}
	return false
}
