package app

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"pentgo/internal/adapters/skillfs"
	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

func newCatalogFixture(t *testing.T, files fstest.MapFS) (*storage.TranscriptStore, *skillfs.Registry) {
	t.Helper()
	store, err := storage.CreateProjectStore(t.TempDir(), "fixture", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	session := domain.NewSession("", "fixture", time.Now().UTC())
	if err := store.SaveSession(session); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	registry := skillfs.NewRegistry(files)
	registry.Scan()
	t.Cleanup(func() {
		_ = transcript.Close()
		_ = store.Close()
	})
	return transcript, registry
}

func TestEnsureSessionSkillCatalogAddsInitialCatalogOnce(t *testing.T) {
	transcript, registry := newCatalogFixture(t, fstest.MapFS{
		"api.md": &fstest.MapFile{Data: []byte("---\ndescription: API routing\n---\n# API\n")},
	})

	if err := ensureSessionSkillCatalog(transcript, registry); err != nil {
		t.Fatal(err)
	}
	if err := ensureSessionSkillCatalog(transcript, registry); err != nil {
		t.Fatal(err)
	}

	messages := transcript.Messages()
	if len(messages) != 1 || messages[0].Role != agent.RoleSystem || !strings.Contains(messages[0].Content, "`api`：API routing") {
		t.Fatalf("messages = %#v", messages)
	}
	if digest, ok := catalogDigestFromMessage(messages[0]); !ok || digest != registry.Digest() {
		t.Fatalf("catalog digest/ok = %q/%t", digest, ok)
	}
}

func TestEnsureSessionSkillCatalogAppendsChangedReplacement(t *testing.T) {
	transcript, oldRegistry := newCatalogFixture(t, fstest.MapFS{
		"api.md": &fstest.MapFile{Data: []byte("---\ndescription: Old API routing\n---\n# API\n")},
	})
	if err := ensureSessionSkillCatalog(transcript, oldRegistry); err != nil {
		t.Fatal(err)
	}
	replacement := skillfs.NewRegistry(fstest.MapFS{
		"api.md": &fstest.MapFile{Data: []byte("---\ndescription: New API routing\n---\n# API\n")},
	})
	replacement.Scan()

	if err := ensureSessionSkillCatalog(transcript, replacement); err != nil {
		t.Fatal(err)
	}

	messages := transcript.Messages()
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "completely replaces every earlier") || !strings.Contains(messages[1].Content, "New API routing") {
		t.Fatalf("messages = %#v", messages)
	}
	if digest, ok := catalogDigestFromMessage(messages[1]); !ok || digest != replacement.Digest() {
		t.Fatalf("catalog digest/ok = %q/%t", digest, ok)
	}
}

func TestEnsureSessionSkillCatalogWithdrawsOldCatalogWhenCurrentCatalogIsEmpty(t *testing.T) {
	transcript, oldRegistry := newCatalogFixture(t, fstest.MapFS{
		"api.md": &fstest.MapFile{Data: []byte("---\ndescription: API routing\n---\n# API\n")},
	})
	if err := ensureSessionSkillCatalog(transcript, oldRegistry); err != nil {
		t.Fatal(err)
	}
	emptyRegistry := skillfs.NewRegistry(fstest.MapFS{})
	emptyRegistry.Scan()

	if err := ensureSessionSkillCatalog(transcript, emptyRegistry); err != nil {
		t.Fatal(err)
	}

	messages := transcript.Messages()
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "No PentGo skills are currently available") {
		t.Fatalf("messages = %#v", messages)
	}
	if digest, ok := catalogDigestFromMessage(messages[1]); !ok || digest != "" {
		t.Fatalf("catalog digest/ok = %q/%t", digest, ok)
	}
}

func TestCatalogDigestFromMessageRejectsNonSystemAndMalformedMarkers(t *testing.T) {
	for _, message := range []agent.Message{
		{Role: agent.RoleUser, Content: `<pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">`},
		{Role: agent.RoleSystem, Content: `prefix <pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">`},
		{Role: agent.RoleSystem, Content: `<pentgo-skill-catalog digest="not-a-digest">`},
	} {
		if _, ok := catalogDigestFromMessage(message); ok {
			t.Fatalf("accepted malformed/non-system message %#v", message)
		}
	}
}
