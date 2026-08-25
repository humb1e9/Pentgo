package runtime

import (
	"context"
	"testing"
	"testing/fstest"
)

func TestSkillDiagnosticsAreShownOncePerProject(t *testing.T) {
	root := t.TempDir()
	skills := fstest.MapFS{"bad.md": &fstest.MapFile{Data: []byte("# missing metadata\n")}}
	first := NewManager(DefaultConfig(), root, Dependencies{SkillsFS: skills})
	if _, _, err := first.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := first.SkillDiagnostics(); len(got) != 1 {
		t.Fatalf("first diagnostics = %#v, want one", got)
	}
	if err := first.CloseProject(); err != nil {
		t.Fatal(err)
	}

	resumed := NewManager(DefaultConfig(), root, Dependencies{SkillsFS: skills})
	defer resumed.CloseProject()
	if _, err := resumed.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := resumed.SkillDiagnostics(); len(got) != 0 {
		t.Fatalf("resumed diagnostics = %#v, want none", got)
	}
}
