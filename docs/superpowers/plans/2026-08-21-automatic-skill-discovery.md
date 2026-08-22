# Session-Scoped Automatic Skill Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scan valid local skills once at PentGo startup, automatically inject a compact digest-versioned catalog into each new or resumed session, and let the model load only matching skill bodies on demand.

**Architecture:** `skillfs.Registry` owns one startup-built in-memory metadata/path allowlist, catalog renderer, and SHA-256 digest; it never retains a full skill body and never writes an index file. `Coordinator` injects a host-authored `RoleSystem` catalog message into a transcript at session creation/resume only when the latest persisted catalog digest differs from the startup digest. Every future turn simply replays that transcript context, so it performs neither a filesystem scan nor a catalog re-injection.

**Tech Stack:** Go 1.25, standard-library `crypto/sha256`, `io/fs`, `testing/fstest`, `gopkg.in/yaml.v3`, SQLite transcript storage, Eino, Bubble Tea.

## Global Constraints

- Scan top-level `skills/*.md` once per `Coordinator` construction; do not watch files, rescan per turn, or hot-reload skills.
- Do not call an LLM, network service, or external process during scanning, catalog rendering, digesting, or session catalog injection.
- Do not create `skill_index.md`, `skill_index.json`, or any other skill-index file.
- Retain only sorted name/compact-description metadata, catalog digest/rendering, diagnostics, and exact `name -> discovered path` allowlist; never retain all skill bodies.
- Normalize every accepted description's whitespace and deterministically truncate it to 160 UTF-8 bytes before catalog storage, digesting, or rendering; do not use an LLM to summarize it.
- Preserve `load_skill(name)` as the model-facing tool and its one required string name schema; its full body remains lazily read and bounded to 32 KiB.
- Inject catalog context at new-session creation and session resume only; unchanged catalog digests must not create duplicate transcript messages.
- A changed digest must append an explicit replacement catalog; a now-empty catalog must replace an earlier nonempty one on resume.
- Use a host-owned catalog marker with a lower-case SHA-256 digest; never parse model-facing Markdown to identify catalog messages.
- Skip unreadable, malformed, or missing-description skills independently; show diagnostics in local TUI activity only, never in a model transcript.
- Remove the `/load_skill` CLI command but retain the internal model tool `load_skill`.
- Require YAML frontmatter containing a nonempty `description`; do not use an LLM or title fallback for invalid skills.

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/adapters/skillfs/registry.go` | One-time catalog scan, diagnostics, stable digest, host catalog rendering, name/path allowlist, lazy body loading. |
| `internal/adapters/skillfs/registry_test.go` | Registry catalog/digest/renderer tests, bad-file isolation, unavailable filesystem behavior, and lazy-load tests. |
| `internal/app/skill_catalog.go` | Host catalog message framing, digest extraction/validation, and transcript reconciliation helper used by new and resumed sessions. |
| `internal/app/skill_catalog_test.go` | Test initial, idempotent, changed, and empty catalog injection against persisted transcripts. |
| `internal/app/coordinator.go` | Scan exactly once in `New`, retain diagnostics, configure automatic loader, and call the session catalog helper from new/resume paths. |
| `internal/app/coordinator_test.go` | Assert startup scan, first-turn catalog visibility, tool availability, and restart/update behavior. |
| `internal/app/turn_service.go` | Hold the startup-discovered loader/catalog state; never scan or inject a catalog during `RunTurn`. |
| `internal/app/tools.go` | Register model `load_skill` only when the startup registry catalog has one or more valid entries. |
| `internal/app/turn_service_test.go` | Confirm nonempty vs empty automatic catalog tool availability and no catalog mutation during turn execution. |
| `internal/adapters/llm/prompt.go` | Fixed small routing rule; no full catalog, manual command, or per-turn skill summary injection. |
| `internal/adapters/llm/engine_test.go` | Confirm fixed system rule plus persisted session catalog appear in correct replay order. |
| `internal/cli/model.go` | Remove `/load_skill` UI command/copy and show startup diagnostics only as transient local activity. |
| `internal/cli/model_test.go` | Verify old command is unknown/hidden and bad-skill diagnostics are visible but not transcript content. |
| `README.md` | User-level automatic session-catalog semantics. |
| `docs/ARCHITECTURE.md` | Runtime catalog ownership and lifecycle. |
| `docs/TECHNICAL.md` | Tool availability, one-scan lifecycle, catalog context, and restart requirements. |

---

### Task 1: Build a One-Time Metadata Registry With Digest and Catalog Renderer

**Files:**
- Modify: `internal/adapters/skillfs/registry.go:1-191`
- Modify: `internal/adapters/skillfs/registry_test.go:1-28`

**Interfaces:**
- Consumes: injected `fs.FS` rooted at `skills/` and top-level `*.md` skill files.
- Produces:
  ```go
  type Diagnostic struct {
      Path   string
      Reason string
  }

  type ScanResult struct {
      Catalog     []Skill
      Digest      string
      Diagnostics []Diagnostic
  }

  func (registry *Registry) Scan() ScanResult
  func (registry *Registry) Catalog() []Skill
  func (registry *Registry) HasSkills() bool
  func (registry *Registry) Digest() string
  func (registry *Registry) RenderCatalog(replacement bool) string
  func (registry *Registry) Diagnostics() []Diagnostic
  func (registry *Registry) Load(name string) (string, error)
  ```
- Rules: `Scan` is safe to call once during startup but must atomically replace all metadata/path/diagnostic state if called in tests. It never returns an error. The digest is `sha256` over sorted JSON-equivalent `name + "\n" + description + "\n"` pairs. A nonempty catalog renderer contains only names/descriptions and routing instructions; it has no body content. An empty renderer is valid only for `replacement=true` and explicitly withdraws old skills.

- [ ] **Step 1: Write failing registry tests for sorted metadata, a stable digest, and a compact renderer**

Replace the current test with the following test and add `reflect` to imports:

```go
func TestRegistryScanBuildsStableCatalogAndRendersNoBodies(t *testing.T) {
    registry := NewRegistry(fstest.MapFS{
        "zeta.md":  &fstest.MapFile{Data: []byte("---\ndescription: Zeta routing\n---\n# Zeta\n\nZETA BODY\n")},
        "alpha.md": &fstest.MapFile{Data: []byte("---\ndescription: Alpha routing\n---\n# Alpha\n\nALPHA BODY\n")},
    })

    first := registry.Scan()
    second := registry.Scan()

    if got := registry.Catalog(); !reflect.DeepEqual(got, []Skill{{Name: "alpha", Description: "Alpha routing"}, {Name: "zeta", Description: "Zeta routing"}}) {
        t.Fatalf("catalog = %#v", got)
    }
    if len(first.Digest) != 64 || first.Digest != second.Digest || first.Digest != registry.Digest() {
        t.Fatalf("digest = %#v / %#v / %q", first, second, registry.Digest())
    }
    catalog := registry.RenderCatalog(false)
    for _, want := range []string{"<pentgo-skill-catalog digest=\"" + first.Digest + "\">", "`alpha`：Alpha routing", "`zeta`：Zeta routing", "调用 load_skill"} {
        if !strings.Contains(catalog, want) {
            t.Fatalf("catalog missing %q: %q", want, catalog)
        }
    }
    if strings.Contains(catalog, "ALPHA BODY") || strings.Contains(catalog, "ZETA BODY") {
        t.Fatalf("catalog leaked bodies: %q", catalog)
    }
}
```

- [ ] **Step 2: Write failing tests for isolated bad skills, no-title fallback, and unavailable filesystems**

Add imports `io/fs` and `errors`. Add:

```go
type unavailableFS struct{}

func (unavailableFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

func TestRegistryScanSkipsInvalidSkillsAndKeepsValidNeighbors(t *testing.T) {
    registry := NewRegistry(fstest.MapFS{
        "valid.md":          &fstest.MapFile{Data: []byte("---\ndescription: Valid routing\n---\n# Valid\n\nBODY\n")},
        "bad-yaml.md":       &fstest.MapFile{Data: []byte("---\ndescription: [\n---\n# Bad\n")},
        "no-description.md": &fstest.MapFile{Data: []byte("---\nname: missing-description\n---\n# Untitled\n")},
        "no-frontmatter.md": &fstest.MapFile{Data: []byte("# Untitled\n")},
    })

    result := registry.Scan()

    if got := registry.Catalog(); !reflect.DeepEqual(got, []Skill{{Name: "valid", Description: "Valid routing"}}) {
        t.Fatalf("catalog = %#v", got)
    }
    if len(result.Diagnostics) != 3 {
        t.Fatalf("diagnostics = %#v", result.Diagnostics)
    }
    if _, err := registry.Load("bad-yaml"); err == nil {
        t.Fatal("broken skill was loadable")
    }
    if body, err := registry.Load("valid"); err != nil || !strings.Contains(body, "BODY") || strings.Contains(body, "description:") {
        t.Fatalf("body/err = %q/%v", body, err)
    }
}

func TestRegistryScanReportsUnavailableFilesystemAndEmptyReplacement(t *testing.T) {
    registry := NewRegistry(unavailableFS{})

    result := registry.Scan()

    if registry.HasSkills() || registry.Digest() != "" || len(result.Diagnostics) != 1 {
        t.Fatalf("result = %#v", result)
    }
    replacement := registry.RenderCatalog(true)
    if !strings.Contains(replacement, "No PentGo skills are currently available") || !strings.Contains(replacement, "replaces every earlier") {
        t.Fatalf("replacement = %q", replacement)
    }
    if _, err := registry.Load("anything"); err == nil || !errors.Is(err, ErrUnknownSkill) {
        t.Fatalf("load error = %v", err)
    }
}
```

Declare the package error used by the last assertion:

```go
var ErrUnknownSkill = errors.New("unknown skill")
```

Run: `go test ./internal/adapters/skillfs -count=1`

Expected: FAIL because the registry has no digest/renderer/diagnostics interfaces, still requires manual-ready state, and falls back to headings.

- [ ] **Step 3: Replace manual-ready state with atomically published metadata and diagnostic state**

In `internal/adapters/skillfs/registry.go`:

1. Add imports `crypto/sha256`, `encoding/hex`, and `errors`.
2. Declare:

```go
var ErrUnknownSkill = errors.New("unknown skill")

type Diagnostic struct {
    Path   string
    Reason string
}

type ScanResult struct {
    Catalog     []Skill
    Digest      string
    Diagnostics []Diagnostic
}
```

3. Replace `ready bool` with:

```go
catalog     []Skill
paths       map[string]string
digest      string
diagnostics []Diagnostic
```

4. Initialize `paths` in `NewRegistry`.
5. Replace `Loaded` with `HasSkills`, which returns `len(registry.catalog) != 0` under `RLock`.
6. Make `Catalog`, `Diagnostics`, and `ScanResult.Catalog` defensive slice copies.

- [ ] **Step 4: Implement strict scanning and the stable digest**

Replace `Scan() (string, error)` with `Scan() ScanResult`. Create fresh locals:

```go
catalog := []Skill{}
paths := make(map[string]string)
diagnostics := []Diagnostic{}
```

If `registry == nil` or `registry.source == nil`, append `Diagnostic{Path: "skills", Reason: "skill filesystem is unavailable"}`. Otherwise call `fs.Glob(registry.source, "*.md")`; on its error append `Diagnostic{Path: "skills", Reason: err.Error()}`. On a usable glob result, process every path independently:

```go
content, err := fs.ReadFile(registry.source, path)
if err != nil {
    diagnostics = append(diagnostics, Diagnostic{Path: path, Reason: err.Error()})
    continue
}
metadata, _, err := parseDocument(content)
if err != nil {
    diagnostics = append(diagnostics, Diagnostic{Path: path, Reason: err.Error()})
    continue
}
name := strings.TrimSuffix(path, ".md")
catalog = append(catalog, Skill{Name: name, Description: metadata.Description})
paths[name] = path
```

Sort catalog by `Name`. Compute digest with a new helper:

```go
func catalogDigest(catalog []Skill) string {
    if len(catalog) == 0 {
        return ""
    }
    hash := sha256.New()
    for _, skill := range catalog {
        _, _ = hash.Write([]byte(skill.Name))
        _, _ = hash.Write([]byte{'\n'})
        _, _ = hash.Write([]byte(skill.Description))
        _, _ = hash.Write([]byte{'\n'})
    }
    return hex.EncodeToString(hash.Sum(nil))
}
```

Acquire the registry lock once and replace every state field. Return copied catalog/diagnostics and digest. Never retain the body returned by `fs.ReadFile` after the loop iteration.

- [ ] **Step 5: Require valid frontmatter and add catalog rendering**

Replace `parseDocument` with strict validation:

```go
func parseDocument(content []byte) (frontmatter, []byte, error) {
    text := strings.TrimSpace(string(content))
    if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
        return frontmatter{}, nil, fmt.Errorf("YAML frontmatter is required")
    }
    rest := text[3:]
    end := strings.Index(rest, "\n---")
    if end < 0 {
        return frontmatter{}, nil, fmt.Errorf("frontmatter closing delimiter not found")
    }
    var metadata frontmatter
    if err := yaml.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &metadata); err != nil {
        return frontmatter{}, nil, fmt.Errorf("parse frontmatter: %w", err)
    }
    metadata.Description = strings.TrimSpace(metadata.Description)
    if metadata.Description == "" {
        return frontmatter{}, nil, fmt.Errorf("frontmatter description is required")
    }
    return metadata, []byte(strings.TrimSpace(rest[end+len("\n---"):])) , nil
}
```

Delete `heading`. Add `Digest()` and this renderer; call it only after `Scan`:

```go
func (registry *Registry) RenderCatalog(replacement bool) string {
    if registry == nil {
        return ""
    }
    registry.mu.RLock()
    catalog := append([]Skill(nil), registry.catalog...)
    digest := registry.digest
    registry.mu.RUnlock()

    var builder strings.Builder
    fmt.Fprintf(&builder, "<pentgo-skill-catalog digest=\"%s\">\n", digest)
    if replacement {
        builder.WriteString("This catalog completely replaces every earlier PentGo skill catalog in this session.\n")
    }
    if len(catalog) == 0 {
        builder.WriteString("No PentGo skills are currently available. Do not use names from earlier PentGo skill catalogs.\n")
    } else {
        builder.WriteString("Available PentGo skills:\n")
        for _, skill := range catalog {
            fmt.Fprintf(&builder, "- `%s`：%s\n", skill.Name, skill.Description)
        }
        builder.WriteString("When the task clearly matches a listed skill, call load_skill with its exact name before specialized work. Do not guess skill names; if no entry matches, continue normally.\n")
    }
    builder.WriteString("</pentgo-skill-catalog>")
    return bound([]byte(builder.String()))
}
```

Implement `Load` by snapshotting its selected path under `RLock`; if `name` is absent return `fmt.Errorf("%w: %q", ErrUnknownSkill, name)`. Read only that exact stored path, parse it with the same strict parser, and return `bound(body)`. Do not test or emit `ready` / “run /load_skill first”.

- [ ] **Step 6: Format, run registry tests, and commit**

Run: `gofmt -w internal/adapters/skillfs/registry.go internal/adapters/skillfs/registry_test.go && go test ./internal/adapters/skillfs -count=1`

Expected: PASS.

```bash
git add internal/adapters/skillfs/registry.go internal/adapters/skillfs/registry_test.go
git commit -m "feat: build startup skill catalog metadata"
```

### Task 2: Add Digest-Idempotent Session Catalog Injection

**Files:**
- Create: `internal/app/skill_catalog.go`
- Create: `internal/app/skill_catalog_test.go`

**Interfaces:**
- Consumes: `*storage.TranscriptStore`, `*skillfs.Registry`.
- Produces:
  ```go
  func ensureSessionSkillCatalog(transcript *storage.TranscriptStore, registry *skillfs.Registry) error
  func catalogDigestFromMessage(message agent.Message) (string, bool)
  ```
- Rules: `ensureSessionSkillCatalog` examines only messages with `RoleSystem`; it selects the last valid `<pentgo-skill-catalog digest="..."></pentgo-skill-catalog>` marker. It appends nothing if the last digest equals `registry.Digest()`. It appends `registry.RenderCatalog(false)` if no prior marker exists and the registry is nonempty. It appends `registry.RenderCatalog(true)` if the old/new digest differs, including new digest `""` when withdrawing an old nonempty catalog. It does not append an empty initial catalog.

- [ ] **Step 1: Write failing tests for initial, idempotent, changed, and empty replacement catalogs**

Create `internal/app/skill_catalog_test.go` with an isolated project/transcript fixture. Start with:

```go
func newCatalogFixture(t *testing.T, files fstest.MapFS) (*storage.ProjectStore, *storage.TranscriptStore, *skillfs.Registry) {
    t.Helper()
    store, err := storage.CreateProjectStore(t.TempDir(), "fixture", time.Now().UTC())
    if err != nil {
        t.Fatal(err)
    }
    session := domain.NewSession("", "fixture", time.Now().UTC())
    if err := store.SaveSession(session); err != nil {
        t.Fatal(err)
    }
    transcript, err := store.OpenTranscript(session.ID)
    if err != nil {
        t.Fatal(err)
    }
    registry := skillfs.NewRegistry(files)
    registry.Scan()
    t.Cleanup(func() { _ = transcript.Close(); _ = store.Close() })
    return store, transcript, registry
}
```

Add separate tests:

```go
func TestEnsureSessionSkillCatalogAddsInitialCatalogOnce(t *testing.T) {
    _, transcript, registry := newCatalogFixture(t, fstest.MapFS{
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
}

func TestEnsureSessionSkillCatalogAppendsChangedReplacement(t *testing.T) {
    _, transcript, oldRegistry := newCatalogFixture(t, fstest.MapFS{
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
}

func TestEnsureSessionSkillCatalogWithdrawsOldCatalogWhenStartupCatalogIsEmpty(t *testing.T) {
    _, transcript, oldRegistry := newCatalogFixture(t, fstest.MapFS{
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
}
```

Run: `go test ./internal/app -run TestEnsureSessionSkillCatalog -count=1`

Expected: FAIL because the helper does not exist.

- [ ] **Step 2: Implement marker validation and reconciler**

Create `internal/app/skill_catalog.go`:

```go
package app

import (
    "fmt"
    "regexp"

    skillsadapter "pentgo/internal/adapters/skillfs"
    "pentgo/internal/adapters/storage"
    "pentgo/internal/agent"
)

var skillCatalogMarker = regexp.MustCompile(`\A<pentgo-skill-catalog digest="([a-f0-9]{64})">`)
var emptySkillCatalogMarker = regexp.MustCompile(`\A<pentgo-skill-catalog digest="">`)

func catalogDigestFromMessage(message agent.Message) (string, bool) {
    if message.Role != agent.RoleSystem {
        return "", false
    }
    if match := skillCatalogMarker.FindStringSubmatch(message.Content); match != nil {
        return match[1], true
    }
    return "", emptySkillCatalogMarker.MatchString(message.Content)
}

func ensureSessionSkillCatalog(transcript *storage.TranscriptStore, registry *skillsadapter.Registry) error {
    if transcript == nil {
        return fmt.Errorf("session transcript is unavailable")
    }
    currentDigest := ""
    if registry != nil {
        currentDigest = registry.Digest()
    }
    priorDigest, found := "", false
    for _, message := range transcript.Messages() {
        if digest, ok := catalogDigestFromMessage(message); ok {
            priorDigest, found = digest, true
        }
    }
    if found && priorDigest == currentDigest {
        return nil
    }
    if !found && currentDigest == "" {
        return nil
    }
    replacement := found
    content := ""
    if registry == nil {
        content = `<pentgo-skill-catalog digest="">` + "\nNo PentGo skills are currently available. Do not use names from earlier PentGo skill catalogs.\n</pentgo-skill-catalog>"
    } else {
        content = registry.RenderCatalog(replacement)
    }
    return transcript.Append(agent.Message{Role: agent.RoleSystem, Content: content})
}
```

Keep the regexp anchored to the beginning so ordinary model/user content cannot be treated as a host catalog. The stored message is system role and host-created only; no user-supplied message is appended through this helper.

- [ ] **Step 3: Run focused catalog tests and commit**

Run: `gofmt -w internal/app/skill_catalog.go internal/app/skill_catalog_test.go && go test ./internal/app -run TestEnsureSessionSkillCatalog -count=1`

Expected: PASS.

```bash
git add internal/app/skill_catalog.go internal/app/skill_catalog_test.go
git commit -m "feat: inject idempotent session skill catalogs"
```

### Task 3: Scan Once at Coordinator Construction and Inject at Session Boundaries

**Files:**
- Modify: `internal/app/coordinator.go:36-85,168-242,275-310`
- Modify: `internal/app/coordinator_test.go:1-234`
- Modify: `internal/app/turn_service.go:22-57,68-118`
- Modify: `internal/app/tools.go:17-61`
- Modify: `internal/app/turn_service_test.go:135-153`

**Interfaces:**
- Consumes: `skillfs.Registry.Scan() ScanResult`, `ensureSessionSkillCatalog`, `Registry.HasSkills`, `Registry.Load`, and `Registry.Diagnostics`.
- Produces:
  ```go
  func (coordinator *Coordinator) SkillDiagnostics() []skillsadapter.Diagnostic
  func (service *TurnService) SetSkillCatalog(load SkillLoader, available bool)
  ```
- Rules: `New` scans one time. Neither `openStore` nor `RunTurn` invokes `Scan`. `NewSession` and `ResumeSession` call `ensureSessionSkillCatalog` before returning their session. `load_skill` is available for all turns only when the startup registry has valid entries; catalog content is sourced exclusively from transcript replay, not `TurnInput.SkillSummary`.

- [ ] **Step 1: Write a failing test proving one startup scan and first-user-message ordering**

Add a small instrumented `fs.FS` wrapper in `internal/app/coordinator_test.go` that increments an atomic counter when its root is opened. Construct `Coordinator` with one valid `api.md` file. Assert the counter changes during `New`, then open/create workspace, create a session, submit `"检查 API"`, and assert the counter did not increase after `New`.

Assert transcript order after the first turn:

```go
messages := coordinator.Messages(session.ID)
if len(messages) < 3 || messages[0].Role != agent.RoleSystem || !strings.Contains(messages[0].Content, "`api`：API routing") || messages[1].Role != agent.RoleUser {
    t.Fatalf("messages = %#v", messages)
}
```

Make `coordinatorModel.WithTools` store each model-visible `ToolInfo.Name`, then assert the first model call contained `load_skill`. This test must never call `LoadSkills` and must not inspect `SkillSummary`.

- [ ] **Step 2: Write failing tests for unchanged and changed resume semantics**

Create a workspace using a first Coordinator whose `SkillsFS` has `api.md` with `Old routing`; create a session and close it. Create a second Coordinator over the same root with unchanged skills, open project, `ResumeSession(id)`, and assert one catalog system message remains. Close it.

Create a third Coordinator over the same root but with `api.md` description `New routing`; open/resume and assert the transcript has two system catalog messages, with the second containing both `completely replaces every earlier` and `New routing`.

Run: `go test ./internal/app -run 'TestCoordinator.*SkillCatalog' -count=1`

Expected: FAIL because discovery remains manual and no session-boundary injection exists.

- [ ] **Step 3: Scan exactly once in `New` and retain diagnostics**

Add fields to `Coordinator`:

```go
skills           *skillsadapter.Registry
skillDiagnostics []skillsadapter.Diagnostic
skillAvailable   bool
```

In `New`, construct registry then scan immediately:

```go
registry := skillsadapter.NewRegistry(deps.SkillsFS)
result := registry.Scan()
return &Coordinator{
    cfg: cfg, root: strings.TrimSpace(outputRoot), deps: deps,
    skills: registry,
    skillDiagnostics: append([]skillsadapter.Diagnostic(nil), result.Diagnostics...),
    skillAvailable: registry.HasSkills(),
}
```

Delete `LoadSkills` completely. Add `SkillDiagnostics()` returning a copied slice under `RLock`. Do not clear diagnostics in `closeProjectLocked`: they describe this immutable process-start scan and must remain available until Coordinator disposal.

- [ ] **Step 4: Configure the model loader once and remove all per-turn summary plumbing**

Change `TurnService` fields from `loadSkill SkillLoader; skillSummary string` to `loadSkill SkillLoader; skillsAvailable bool`.

Replace `SetSkillLoader` with:

```go
func (service *TurnService) SetSkillCatalog(load SkillLoader, available bool) {
    if service == nil {
        return
    }
    service.configMu.Lock()
    defer service.configMu.Unlock()
    service.loadSkill = load
    service.skillsAvailable = available
}
```

Make `skillConfig()` return `(SkillLoader, bool)`. In `RunTurn`, build `newRuntimeToolProvider(projectRuntime, session, externalTools, loadSkill, skillsAvailable)` and pass `SkillSummary: ""` to `agent.TurnInput`. Do not call a registry method anywhere in `RunTurn`.

Modify `runtimeToolProvider` to hold `skillsAvailable bool`, accept it in its constructor, and register `load_skill` only when `loadSkill != nil && skillsAvailable`. Update comments to say this loader comes from process-start discovery.

In `openStore`, after creating `service`, do only:

```go
var loadSkill SkillLoader
if coordinator.skillAvailable {
    loadSkill = coordinator.skills.Load
}
service.SetSkillCatalog(loadSkill, coordinator.skillAvailable)
```

Delete the old `Loaded`/`Summary` block entirely.

- [ ] **Step 5: Inject on `NewSession` and `ResumeSession` before returning**

In `Coordinator.NewSession`, after `runtime.NewSession` succeeds, call:

```go
if err := ensureSessionSkillCatalog(runtime.Transcript(session.ID), coordinator.skills); err != nil {
    return nil, err
}
return runtime.Snapshot(session.ID), nil
```

Do not return the original `runtime.NewSession` clone because it was obtained before catalog injection; use `Snapshot` so state remains consistent.

In `Coordinator.ResumeSession`, after verifying `session := runtime.Snapshot(id)` is nonnil, call:

```go
if err := ensureSessionSkillCatalog(runtime.Transcript(id), coordinator.skills); err != nil {
    return nil, err
}
return runtime.Snapshot(id), nil
```

This makes both the CLI resume path and explicit in-app resume use the exact same idempotent behavior.

- [ ] **Step 6: Update tool-provider tests and prove turns never append a catalog**

Replace `TestRuntimeToolsExposeOnlyWriteProjectFact` with two tests:

1. A provider created with a stub loader and `skillsAvailable=true` has `write_project_fact` and `load_skill`.
2. A provider with nil loader and `skillsAvailable=false` has only `write_project_fact`.

Add a TurnService test that seeds a transcript with one `RoleSystem` catalog message, executes a scripted no-tool turn, and asserts its message count changes only by one user and one assistant event—not by a second system catalog. This is the regression boundary that enforces session-level rather than turn-level injection.

- [ ] **Step 7: Format, run app tests, and commit**

Run: `gofmt -w internal/app/coordinator.go internal/app/coordinator_test.go internal/app/turn_service.go internal/app/tools.go internal/app/turn_service_test.go && go test ./internal/app -count=1`

Expected: PASS.

```bash
git add internal/app/coordinator.go internal/app/coordinator_test.go internal/app/turn_service.go internal/app/tools.go internal/app/turn_service_test.go
git commit -m "feat: attach startup skill catalog to sessions"
```

### Task 4: Make Fixed Model Instructions Route Through Session Catalogs

**Files:**
- Modify: `internal/adapters/llm/prompt.go:8-47`
- Modify: `internal/adapters/llm/engine_test.go:80-112`

**Interfaces:**
- Consumes: one host-authored `agent.RoleSystem` catalog message in the persistent transcript.
- Produces: fixed system instruction that requires exact-name `load_skill` routing but does not receive a `SkillSummary` parameter.
- Rules: the fixed prompt is compact and invariant per turn; `toSchemaMessages` preserves the session catalog message after that fixed prompt and before user messages.

- [ ] **Step 1: Write failing engine assertions for fixed routing rule and session catalog order**

Update `TestEinoEngineUsesChineseSystemPrompt` to run with messages:

```go
messages := []agent.Message{
    {Role: agent.RoleSystem, Content: `<pentgo-skill-catalog digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">\n- ` + "`api`：API routing\n</pentgo-skill-catalog>"},
    {Role: agent.RoleUser, Content: "检查 API"},
}
```

After draining events, assert:

```go
input := model.inputs[0]
if len(input) != 3 || input[0].Role != schema.System || input[1].Role != schema.System || input[2].Role != schema.User {
    t.Fatalf("input = %#v", input)
}
for _, want := range []string{"会话上下文存在 PentGo 技能目录", "调用 load_skill", "不要猜测技能名称"} {
    if !strings.Contains(input[0].Content, want) {
        t.Fatalf("fixed prompt missing %q: %q", want, input[0].Content)
    }
}
if strings.Contains(input[0].Content, "`api`：API routing") || strings.Contains(input[0].Content, "/load_skill") {
    t.Fatalf("fixed prompt must not repeat catalog or CLI command: %q", input[0].Content)
}
if !strings.Contains(input[1].Content, "`api`：API routing") {
    t.Fatalf("catalog replay missing: %#v", input)
}
```

Run: `go test ./internal/adapters/llm -run TestEinoEngineUsesChineseSystemPrompt -count=1`

Expected: FAIL because the old prompt conditionally embeds `SkillSummary` and requests manual `/load_skill` initialization.

- [ ] **Step 2: Remove `skillSummary` from the prompt interface and write fixed routing policy**

Change the signature to:

```go
func SystemPrompt(input string, projectFacts string) string
```

Update `engine.go` call to:

```go
instruction := SystemPrompt(input.SystemPrompt, input.ProjectFacts)
```

Delete both `skillSummary` conditional branches. After the tool-use section in `baseSystemPrompt`, add exactly this Chinese policy:

```text
- 当会话上下文存在 PentGo 技能目录且当前任务明确匹配其中某项技能时，必须先用目录中列出的准确名称调用 load_skill，再执行专用工作；不得猜测技能名称。没有匹配项时继续常规工作。
```

This string intentionally does not use the slash-command spelling and does not duplicate any catalog entry.

- [ ] **Step 3: Run focused adapter tests and commit**

Run: `gofmt -w internal/adapters/llm/prompt.go internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go && go test ./internal/adapters/llm -count=1`

Expected: PASS.

```bash
git add internal/adapters/llm/prompt.go internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go
git commit -m "feat: route model through session skill catalogs"
```

### Task 5: Remove Manual CLI Initialization and Surface Startup Diagnostics

**Files:**
- Modify: `internal/cli/model.go:109-132,333-342,379-435`
- Modify: `internal/cli/model_test.go:237-258`

**Interfaces:**
- Consumes: `Coordinator.SkillDiagnostics() []skillfs.Diagnostic` from the immutable process-start scan.
- Produces: no `/load_skill` CLI path, transient diagnostic entries only, and unchanged internal `load_skill` model tool availability.
- Rules: diagnostic entries are never passed to `TranscriptStore.Append`; constructing a terminal model may add each diagnostic exactly once to its local `activity` collection.

- [ ] **Step 1: Write failing tests for removed discoverability, unknown command, and local-only diagnostics**

Update `TestTerminalModelShowsWelcomeCardForEmptySession` so expected strings are `PentGo`, `准备开始`, `/new`, `/help`, `Enter 发送`, and `Ctrl+O 详情`; assert no `/load_skill` is present:

```go
if strings.Contains(view, "/load_skill") {
    t.Fatalf("obsolete command remains visible:\n%s", view)
}
```

Add:

```go
func TestTerminalModelRejectsObsoleteLoadSkillCommand(t *testing.T) {
    model := newTerminalModel(context.Background(), nil, "")
    model.handleLine("/load_skill")
    if !model.hasActivity(activityError, "错误：未知命令") {
        t.Fatalf("activity = %#v", model.activity)
    }
}
```

Create a real coordinator with a bad `fstest.MapFS` skill, open workspace/create a session, construct `newTerminalModel`, then assert its view contains `技能已跳过：bad.md` and `frontmatter`. Assert `coordinator.Messages(session.ID)` has one system catalog only when valid skills exist; in this bad-only fixture it must remain empty, proving the warning never entered the transcript.

Run: `go test ./internal/cli -run 'TestTerminalModel(ShowsWelcomeCardForEmptySession|RejectsObsoleteLoadSkillCommand|ShowsSkillDiagnostics)' -count=1`

Expected: FAIL because `/load_skill` is still visible/accepted and diagnostics are not displayed.

- [ ] **Step 2: Delete `/load_skill` CLI handling and stale UI copy**

In `internal/cli/model.go`:

1. Delete the full `case "/load_skill":` branch, including `coordinator.LoadSkills()`.
2. Change welcome hint to:
   ```go
   mutedStyle.Render("/new 新建会话  ·  /help 查看命令")
   ```
3. Change help string to:
   ```go
   model.addActivity(activityInfo, "/new  /session rename|list|delete  /status  /blackboard  /clear  /exit")
   ```
4. Retain the existing default branch, which returns `错误：未知命令`.

- [ ] **Step 3: Display copied startup diagnostics once in the terminal model**

At the end of `newTerminalModel`, after `model.refresh()` and before return, add:

```go
if coordinator != nil {
    for _, diagnostic := range coordinator.SkillDiagnostics() {
        model.addActivity(activityError, fmt.Sprintf("技能已跳过：%s：%s", diagnostic.Path, diagnostic.Reason))
    }
    model.refresh()
}
```

Do not write any diagnostic through `Coordinator.Submit`, `ProjectRuntime`, or `TranscriptStore`.

- [ ] **Step 4: Run full CLI tests and commit**

Run: `gofmt -w internal/cli/model.go internal/cli/model_test.go && go test ./internal/cli -count=1`

Expected: PASS.

```bash
git add internal/cli/model.go internal/cli/model_test.go
git commit -m "feat: remove manual skill command"
```

### Task 6: Update Documentation to Describe Session-Level Catalogs

**Files:**
- Modify: `README.md:44,91-104,145-147`
- Modify: `docs/ARCHITECTURE.md:109-118`
- Modify: `docs/TECHNICAL.md:104-109,125-135`

**Interfaces:**
- Consumes: completed Tasks 1–5.
- Produces: user/maintainer documentation aligned with one startup scan, session-level catalog injection, lazy loading, diagnostics, and restart-only changes.

- [ ] **Step 1: Update README startup and interactive command table**

At README line 44, explain that `skills/` remains beside the startup directory because PentGo scans it locally once during startup. Remove the `/load_skill` table row.

Replace its Skills section with exactly:

```markdown
## Skills

PentGo 在启动时本地扫描启动目录中的 `skills/*.md`，从每份有效技能的 YAML frontmatter 读取 `description`，并建立当前进程的轻量名称/路径目录；此过程不调用模型或网络服务。每个新建或恢复的会话会自动获得一条精简技能目录上下文。任务明确匹配目录项时，模型会使用准确名称调用 `load_skill(name)`，按需读取单份正文。

同一会话的后续 turn 复用已持久化的目录上下文，不会重新扫描或重复注入。修改、增删或修复技能后，请重启 PentGo；恢复会话时，新目录会明确替换该会话此前的 PentGo 技能目录。

格式错误或无法读取的技能会被跳过，并在启动后的终端活动中显示文件名和原因；其余有效技能保持可用。
```

- [ ] **Step 2: Update architecture and technical lifecycle descriptions**

Replace `docs/ARCHITECTURE.md` Skills paragraph with:

```markdown
`skillfs.Registry` 构造时接收显式 `fs.FS`，不读取当前工作目录，也不使用包级默认 registry。Coordinator 每次启动时本地扫描一次顶层 `*.md`，用有效 YAML frontmatter 描述建立内存名称/路径白名单和稳定 digest；坏文件被诊断并跳过。新建或恢复会话时，宿主将 digest 版本化的精简目录作为一条 system transcript message 注入；未变更目录不会重复注入，变更目录会显式替换旧目录。模型依据该会话上下文以准确名称调用 `load_skill` 后才读取单个正文。
```

In `docs/TECHNICAL.md`:

- replace the application-tool bullet with `- 应用层在启动时发现至少一个有效技能后，向模型提供 load_skill。`;
- state section 7's scan happens once per process startup, a compact catalog is attached on session create/resume, later turns replay it, and runtime skill changes require restart;
- retain the exact `skills/*.md` root path and 32 KiB lazy-body limit;
- state that invalid skills are skipped with local TUI diagnostics.

- [ ] **Step 3: Verify active documentation no longer advertises the slash command or index file**

Run: `grep -RIn --exclude-dir=.git --exclude='2026-08-09-summary-only-skills.md' --exclude='2026-08-20-claude-code-tui-refresh.md' --exclude='2026-08-10-agent-runtime-redesign.md' -E '/load_skill|skill_index' README.md docs/ARCHITECTURE.md docs/TECHNICAL.md internal cmd`

Expected: no `/load_skill` command copy or `skill_index` references. Matches for the internal `load_skill` tool name without a slash are allowed.

- [ ] **Step 4: Commit documentation**

```bash
git add README.md docs/ARCHITECTURE.md docs/TECHNICAL.md
git commit -m "docs: describe session skill catalogs"
```

### Task 7: Run Full Regression and Build Verification

**Files:**
- Verify only; do not add unrelated files.

**Interfaces:**
- Consumes: all preceding commits.
- Produces: a formatted, race-clean, buildable repository with no diff whitespace faults.

- [ ] **Step 1: Run all tests**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 2: Run race detection**

Run: `go test ./... -race -count=1`

Expected: PASS.

- [ ] **Step 3: Build and vet**

Run: `go build ./... && go vet ./...`

Expected: both commands exit 0.

- [ ] **Step 4: Verify formatting and whitespace**

Run: `gofmt -l internal/adapters/skillfs/registry.go internal/adapters/skillfs/registry_test.go internal/app/skill_catalog.go internal/app/skill_catalog_test.go internal/app/coordinator.go internal/app/coordinator_test.go internal/app/turn_service.go internal/app/tools.go internal/app/turn_service_test.go internal/adapters/llm/prompt.go internal/adapters/llm/engine.go internal/adapters/llm/engine_test.go internal/cli/model.go internal/cli/model_test.go && git diff --check`

Expected: `gofmt -l` emits no paths and `git diff --check` emits no errors.

- [ ] **Step 5: Commit an actual verification repair only if one exists**

```bash
git status --short
git add -u
git commit -m "chore: verify session skill catalogs"
```

Create this commit only if verification required a tracked-file repair. Otherwise leave the working tree unchanged.

## Self-Review

- **Spec coverage:** Task 1 creates the one-process-only metadata/path registry, strict formatting, diagnostics, digest, and renderer. Task 2 makes catalog context a digest-idempotent persisted session message, including empty replacement semantics. Task 3 puts scanning in `Coordinator.New`, injects at new/resume only, and proves no turn scan/injection occurs. Task 4 gives the model stable routing instructions without embedding catalog data per turn. Task 5 removes manual initialization and presents local diagnostics. Task 6 documents all lifecycle, restart, and no-index behavior. Task 7 validates the repository.
- **Placeholder scan:** Every task declares exact paths, exported/internal interfaces, test setup/assertions, implementation snippets, commands, and expected outcomes. No deferred work labels remain.
- **Type consistency:** Task 1 defines `skillfs.Diagnostic`, `ScanResult`, registry digest/rendering APIs, and `ErrUnknownSkill`; Task 2 consumes them through `ensureSessionSkillCatalog`; Task 3 invokes that helper and replaces summary state with boolean availability; Task 4 removes the remaining `SkillSummary` prompt path; later tasks use only `load_skill` for the internal model tool and `/load_skill` for the removed CLI command.
