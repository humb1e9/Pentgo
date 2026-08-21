# CyberStrike Directory Skill Discovery Implementation Plan

> Superseded by `2026-08-09-summary-only-skills.md`, which keeps the Bingo skill files flat and uses a two-stage summary/body loader.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the static `skills/index.md` dependency and discover progressive-disclosure skills from CyberStrikeAI-style `skills/<name>/SKILL.md` directories.

**Architecture:** Each immediate child directory of `skills/` becomes a skill when it contains `SKILL.md`. The registry scans embedded directories, uses YAML frontmatter descriptions when present, falls back to the first Markdown heading, and renders a runtime summary from the discovered catalog. Supporting files live beside `SKILL.md` and are loaded through the existing third-stage resource tool.

**Tech Stack:** Go `embed.FS`, `gopkg.in/yaml.v3`, Markdown skill assets, existing Eino tool runtime.

## Global Constraints

- Keep all current skill names and bodies available under directory names.
- Do not use a checked-in index file as the runtime catalog.
- Load only `SKILL.md` metadata at discovery time; load bodies and supporting resources on demand.
- Reject resource paths outside the selected skill directory.

---

### Task 1: Migrate Skill Assets to Directory Layout

**Files:**
- Move: `skills/*.md` (except `index.md`) to `skills/<name>/SKILL.md`
- Move: `skills/resources/<name>/*` to `skills/<name>/...`
- Delete: `skills/index.md`

- [x] **Step 1: Move skill bodies and resources**

Create one directory per existing skill name, move its body to `SKILL.md`, move references under the same directory, and remove the old routing index and shared resource root.

- [x] **Step 2: Verify the asset layout**

Run: `find skills -mindepth 2 -name SKILL.md | wc -l && find skills -maxdepth 1 -name '*.md' | wc -l`
Expected: the first count equals the prior skill count and the second count is zero.

### Task 2: Scan Directories and Build Metadata Summary

**Files:**
- Modify: `skills/registry.go`
- Test: `skills/registry_test.go`

**Interfaces:**
- Produce `skills.Summary() (string, error)` from scanned catalog metadata.
- Keep `skills.Catalog`, `skills.Names`, `skills.Load`, and `skills.LoadResource` compatible with the existing runtime callers.

- [x] **Step 1: Add failing directory-discovery tests**

Assert that catalog entries come from `*/SKILL.md`, `Summary` contains frontmatter and fallback descriptions, `Load("index")` returns an unknown-skill error, and resources resolve below `skills/<name>/`.

- [x] **Step 2: Run focused tests and confirm the old flat/index implementation fails**

Run: `go test ./skills -count=1`
Expected: FAIL because the current registry still reads top-level Markdown and `index.md`.

- [x] **Step 3: Implement embedded directory scanning**

Embed immediate skill directory children, parse optional YAML frontmatter, use the directory basename as the stable skill name, and generate the summary from sorted metadata.

- [x] **Step 4: Update resource resolution**

Resolve validated relative resource paths below the selected skill directory and keep the existing content size bound.

- [x] **Step 5: Run focused registry tests**

Run: `gofmt -w skills/registry.go skills/registry_test.go && go test ./skills -count=1`
Expected: PASS.

### Task 3: Use the Generated Summary in the Agent Prompt

**Files:**
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `README.md`
- Modify: `skills/resources/README.md` (removed with the shared resource root)

- [x] **Step 1: Update prompt tests**

Assert that the initial prompt includes the generated summary and three-stage instructions, and contains no reference to `index.md` or an index tool call.

- [x] **Step 2: Implement summary injection**

Replace `skills.Index()` with `skills.Summary()` and describe directory discovery in the prompt.

- [x] **Step 3: Update user-facing documentation**

Document `skills/<name>/SKILL.md`, frontmatter descriptions, and colocated supporting resources.

- [x] **Step 4: Run focused runtime tests**

Run: `gofmt -w internal/runtime/loop/prompt.go internal/runtime/loop/prompt_test.go && go test ./internal/runtime/loop -count=1`
Expected: PASS.

### Task 4: Full Verification

- [x] **Step 1: Run the complete verification suite**

Run: `go test ./... -race -count=1 && go build ./... && go vet ./... && git diff --check`
Expected: all commands exit 0.
