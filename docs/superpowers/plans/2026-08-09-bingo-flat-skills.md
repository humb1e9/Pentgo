# Bingo Flat Skills Implementation Plan

> The summary-only plan supersedes this layout's shared resource layer: see `2026-08-09-summary-only-skills.md`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep only Bingo's 114 migrated skills and expose each body as `skills/<name>.md` with generated runtime metadata.

**Architecture:** Flatten Bingo skill directories into top-level Markdown files while keeping optional references under `skills/resources/<name>/`. The registry scans top-level `*.md` files, parses frontmatter or headings for the stage-one summary, and loads bodies/resources on demand.

**Tech Stack:** Go `embed.FS`, `gopkg.in/yaml.v3`, existing Eino progressive skill tools.

## Global Constraints

- Keep the exact Bingo skill names and migrated bodies.
- Remove PentGo-only `recon`, `terminal`, `waf-bypass`, and `http-403-bypass` skills.
- Keep resources bounded to the named skill resource directory.
- Do not introduce a checked-in index file.

---

### Task 1: Select and Flatten Bingo Assets

**Files:**
- Move: Bingo skill bodies from `skills/<name>/SKILL.md` to `skills/<name>.md`
- Move: `skills/<name>/references/*` to `skills/resources/<name>/`
- Delete: PentGo-only skill bodies and empty directories

- [x] **Step 1: Verify the Bingo set**

Compare the current directory names with `/home/kali/bingo/bingo/skills/hack-skills`, the five top-level Bingo skills, and the six `local_skills` entries. Expected union: 114 names.

- [x] **Step 2: Flatten bodies and restore shared resource storage**

Move each retained `SKILL.md` to its top-level `<name>.md`, move references to `skills/resources/<name>/`, and remove the four PentGo-only bodies.

- [x] **Step 3: Verify the flat layout**

Run: `find skills -maxdepth 1 -type f -name '*.md' | wc -l && find skills -mindepth 2 -name SKILL.md | wc -l`
Expected: 114 top-level skill Markdown files and zero nested `SKILL.md` files.

### Task 2: Switch Registry Discovery to Top-Level Files

**Files:**
- Modify: `skills/registry.go`
- Modify: `skills/registry_test.go`

**Interfaces:**
- Keep `Catalog`, `Names`, `Summary`, `Load`, and `LoadResource` signatures stable.

- [x] **Step 1: Update registry tests**

Assert the catalog contains Bingo names such as `api_security`, `sqli`, and `advsec-plus`, excludes `recon`, `terminal`, `waf-bypass`, and `http-403-bypass`, and resolves resources from `skills/resources/<name>/`.

- [x] **Step 2: Implement flat-file embedding and discovery**

Use `//go:embed *.md resources`, glob `*.md`, derive names from filenames, and map resource paths below `resources/<skill>/`.

- [x] **Step 3: Run focused registry tests**

Run: `gofmt -w skills/registry.go skills/registry_test.go && go test ./skills -count=1`
Expected: PASS.

### Task 3: Align Prompt and Documentation

**Files:**
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

- [x] **Step 1: Update prompt wording and tests**

Describe the scanned top-level skill summary and `skills/<name>.md` body layout.

- [x] **Step 2: Update documentation**

Document the Bingo-only flat layout and the shared resource convention.

- [x] **Step 3: Run focused runtime tests**

Run: `gofmt -w internal/runtime/loop/prompt.go internal/runtime/loop/prompt_test.go && go test ./internal/runtime/loop -count=1`
Expected: PASS.

### Task 4: Full Verification

- [x] **Step 1: Run the complete verification suite**

Run: `go test ./... -race -count=1 && go build ./... && go vet ./... && git diff --check`
Expected: all commands exit 0.
