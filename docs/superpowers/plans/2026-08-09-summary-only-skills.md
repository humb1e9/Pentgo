# Summary-Only Skill Loading Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce PentGo skill loading to a generated summary plus one matching skill body, with no shared resources layer.

**Architecture:** The user runs `/load_skill` when specialized skill routing is needed. That command scans top-level `skills/*.md` files for frontmatter or heading metadata and caches one bounded `Summary()` string. Turns can start before initialization, but those turns do not expose or call `load_skill`. Each later initialized turn receives the summary in the system prompt, and the model calls `load_skill` for the selected file. Registry, runtime tools, and documentation expose only these two stages.

**Tech Stack:** Go `embed.FS`, `gopkg.in/yaml.v3`, existing Eino tool inference.

## Global Constraints

- Keep the 112 Bingo top-level skill files currently present after removing the two SecSkills files.
- Remove `SecSkills.md`, `SecSkills-main.md`, and the `skills/resources/` tree.
- Remove `read_skill_resource` and third-stage prompt instructions.
- Keep the summary bounded by the existing skill context limit.

---

### Task 1: Remove Non-Bingo and Resource Assets

**Files:**
- Delete: `skills/SecSkills.md`
- Delete: `skills/SecSkills-main.md`
- Delete: `skills/resources/`

- [x] **Step 1: Remove SecSkills and resources**

Delete both SecSkills body files and the shared resources tree.

- [x] **Step 2: Verify the asset set**

Run: `find skills -maxdepth 1 -type f -name '*.md' | wc -l && test ! -e skills/resources`
Expected: 112 top-level Markdown files and no resources directory.

### Task 2: Collapse Runtime Loading to Two Stages

**Files:**
- Modify: `skills/registry.go`
- Modify: `skills/registry_test.go`
- Modify: `internal/runtime/loop/eino_agent.go`
- Modify: `internal/runtime/loop/eino_run_loop_test.go`

- [x] **Step 1: Update registry tests**

Remove resource tests, exclude SecSkills names, and keep summary/body tests.

- [x] **Step 2: Remove resource registry APIs**

Embed only `*.md`, delete `LoadResource`, and retain `Summary` plus `Load`.

- [x] **Step 3: Remove the resource tool**

Delete `readSkillResourceArgs`, resource state, handler, tool description, registration, and tests.

- [x] **Step 4: Run focused tests**

Run: `gofmt -w skills/registry.go skills/registry_test.go internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop_test.go && go test ./skills ./internal/runtime/loop -count=1`
Expected: PASS.

### Task 3: Update Prompt and Documentation

**Files:**
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

- [x] **Step 1: Update prompt tests and text**

Describe the generated summary as stage one and `load_skill` as stage two.

- [x] **Step 2: Update documentation**

Document `skills/<name>.md` plus generated summary and remove resource references.

- [x] **Step 3: Run focused runtime tests**

Run: `gofmt -w internal/runtime/loop/prompt.go internal/runtime/loop/prompt_test.go && go test ./internal/runtime/loop -count=1`
Expected: PASS.

### Task 4: Explicit Initialization Semantics

- [x] **Step 1: Keep plain conversation available before `/load_skill`**

  A turn without a scanned summary keeps the normal built-in tools but omits `load_skill`. Running `/load_skill` refreshes the summary and enables skill-body loading on subsequent turns.

### Task 5: Full Verification

- [x] **Step 1: Run the complete verification suite**

Run: `go test ./... -race -count=1 && go build ./... && go vet ./... && git diff --check`
Expected: all commands exit 0.
