package skillfs

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestSkillRegistryUsesInjectedFS(t *testing.T) {
	registry := NewRegistry(fstest.MapFS{
		"api.md":   &fstest.MapFile{Data: []byte("---\ndescription: API fixture\n---\n# API\n\nUse TARGET.\n")},
		"plain.md": &fstest.MapFile{Data: []byte("# Plain fixture\n")},
	})
	summary, err := registry.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "`api`") || !strings.Contains(summary, "API fixture") {
		t.Fatalf("summary = %q", summary)
	}
	body, err := registry.Load("api")
	if err != nil || strings.HasPrefix(strings.TrimSpace(body), "---") || !strings.Contains(body, "Use TARGET") {
		t.Fatalf("body = %q err=%v", body, err)
	}
	if _, err := registry.Load("../api"); err == nil {
		t.Fatal("path traversal skill accepted")
	}
}
