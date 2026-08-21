# Bingo Skills Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Bingo's `SKILL.md` library available to PentGo's existing `load_skill` tool without replacing PentGo's curated skills.

**Architecture:** Copy Bingo's Markdown skill packages into flat `skills/<name>.md` files and maintain `skills/index.md` as the human-readable catalog. Extend the registry to discover every embedded skill Markdown file, derive stable names and descriptions, and serve them through the existing `Catalog`, `Names`, and `Load` APIs. Existing PentGo names remain unchanged; duplicate names keep the existing curated files.

**Tech Stack:** Go 1.25, `embed.FS`, `io/fs`, Markdown skill files, Go unit tests.

## Global Constraints

- Keep targets, credentials, payloads, and responses as synthetic local CTF fixtures.
- Do not add a Python runtime or Bingo application dependency to PentGo.
- Keep `load_skill` as the only runtime skill-loading contract.
- Preserve current PentGo skill names and contents.
- Limit loaded skill content to the existing 32 KiB context cap.

---

### Task 1: Import Bingo Markdown Skills

**Files:**
- Create: `skills/*.md`
- Create: `skills/index.md`

**Interfaces:**
- Produces ordinary top-level skill files and a checked-in index.

- [x] **Step 1: Copy every Bingo `SKILL.md` into ordinary skill directories**

Run:

```bash
while IFS= read -r -d '' source; do
  name=$(basename "$(dirname "$source")")
  destination="skills/$name.md"
  if [ ! -e "$destination" ]; then
    cp "$source" "$destination"
  fi
done < <(find /home/kali/bingo/bingo/skills -type f -name SKILL.md -print0)
```

Expected: all non-conflicting Bingo Markdown skill files under ordinary `skills/<name>.md` files; existing PentGo skill files remain in place.

- [x] **Step 2: Verify the import contains only Markdown skills**

Run:

```bash
test "$(find /home/kali/bingo/bingo/skills -type f -name SKILL.md | wc -l)" -eq 114
test "$(find skills -maxdepth 1 -type f -name '*.md' ! -name index.md | wc -l)" -ge 118
```

Expected: both commands succeed.

### Task 2: Discover Flat Skills

**Files:**
- Modify: `skills/registry.go`
- Test: `skills/registry_test.go`

**Interfaces:**
- `Catalog() []Skill` includes current entries and imported top-level entries.
- `Names() []string` returns the combined sorted names.
- `Load("index")` returns the routing index; `Load(name string)` loads a concrete curated or imported skill by its ordinary name.

- [x] **Step 1: Add failing registry coverage**

```go
func TestCatalogIncludesImportedBingoSkills(t *testing.T) {
    for _, name := range []string{"api-sec", "api_security", "SecSkills-main"} {
        if _, err := Load(name); err != nil {
            t.Fatalf("Load(%q) error = %v", name, err)
        }
    }
    names := Names()
    if len(names) <= 35 {
        t.Fatalf("names length = %d, want migrated skills", len(names))
    }
}
```

- [x] **Step 2: Run the focused test and observe the failure**

Run: `go test ./skills -run Bingo -count=1`

Expected: FAIL because the imported files are not registered yet.

- [x] **Step 3: Add embedded discovery and stable metadata**

Use `//go:embed *.md`, discover the top-level files with `io/fs.Glob`, skip `index.md`, and expose each filename without `.md` as the skill name. Handle `Load("index")` as a special read of `index.md`. Derive descriptions from the first Markdown heading and fall back to the skill name. Keep curated `descriptions` as the first source of truth and reject unknown names before reading files.

- [x] **Step 4: Run focused registry tests**

Run: `go test ./skills -count=1`

Expected: PASS, including unknown-name and traversal rejection.

### Task 3: Validate Runtime Catalog Integration

**Files:**
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `internal/runtime/loop/prompt_content_test.go`
- Modify: `skills/registry_test.go`

**Interfaces:**
- The existing runner receives the expanded catalog without changes to `SkillLoader` or the Eino tool schema.

- [x] **Step 1: Assert the runtime prompt advertises a migrated skill**

```go
func TestBuildSystemPromptIncludesBingoSkill(t *testing.T) {
    prompt := buildSystemPrompt(skills.Catalog())
    if !strings.Contains(prompt, `name "index"`) {
        t.Fatalf("prompt does not advertise migrated skill")
    }
}
```

- [x] **Step 2: Run loop tests**

Run: `go test ./internal/runtime/loop ./skills -count=1`

Expected: PASS.

- [x] **Step 3: Run the full verification set**

Run: `go test ./... -race -count=1 && go build ./... && go vet ./... && git diff --check`

Expected: all commands exit successfully.

## Self-Review

- All 114 Bingo Markdown skills are copied without bringing in Bingo's Python runtime.
- Curated PentGo names continue to resolve first; imported names are ordinary and collision-free.
- The system prompt contains only index-routing instructions; the model loads `index` and then selects concrete skill bodies from natural language context.
- Registry traversal and size limits remain enforced through the existing `Load` path.
- Runtime tool schemas and session persistence require no changes because they already consume the registry APIs.
