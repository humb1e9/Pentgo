# CyberStrike Progressive Skills Implementation Plan

> Directory discovery supersedes the earlier flat-file implementation; see `2026-08-09-cyberstrike-directory-discovery.md` for the current layout.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Change PentGo skill loading to CyberStrikeAI-style progressive disclosure: metadata at startup, full skill instructions on demand, and supporting resources only when requested.

**Architecture:** Keep the existing flat `skills/*.md` catalog and embed a `skills/resources/<skill>/...` tree for optional references, scripts, and assets. The runtime system prompt carries the generated metadata index, `load_skill` loads one skill body, and `read_skill_resource` loads one validated relative resource path.

**Tech Stack:** Go, `embed.FS`, Eino tool inference, existing PentGo runtime loop and skill registry tests.

## Global Constraints

- PentGo remains a synthetic local CTF tooling project.
- Skill bodies and resources are embedded read-only assets.
- Resource paths must stay inside the named skill resource tree and be bounded by the existing skill context limit.
- Do not load all skill bodies into the initial model context.

---

### Task 1: Add Progressive Skill Registry APIs

**Files:**
- Modify: `skills/registry.go`
- Create: `skills/resources/README.md`
- Test: `skills/registry_test.go`

**Interfaces:**
- Produce `skills.Index() (string, error)` for stage-one metadata.
- Produce `skills.LoadResource(skillName, resourcePath string) (string, error)` for stage-three resources.

- [x] **Step 1: Write tests for index and resource loading**

Add assertions that `Index` contains catalog metadata, `LoadResource` rejects absolute and traversal paths, and a missing resource returns an error.

- [x] **Step 2: Run the focused registry tests and confirm the new APIs fail**

Run: `go test ./skills -run 'Test(Index|LoadResource)' -count=1`
Expected: FAIL because the APIs and resource tree are not present.

- [x] **Step 3: Implement embedded index and resource loading**

Embed `*.md` plus `resources`, expose `Index`, and map a clean relative path to `resources/<skill>/<path>` with size limiting and path validation.

- [x] **Step 4: Run the focused registry tests**

Run: `go test ./skills -run 'Test(Index|LoadResource)' -count=1`
Expected: PASS.

### Task 2: Add the Runtime Third-Stage Tool

**Files:**
- Modify: `internal/runtime/loop/eino_agent.go`
- Test: `internal/runtime/loop/eino_run_loop_test.go`

**Interfaces:**
- Add `readSkillResourceArgs{Skill, Path}`.
- Add `einoToolSet.readSkillResource` and register the `read_skill_resource` Eino tool alongside `load_skill`.

- [x] **Step 1: Write the failing tool registration and behavior tests**

Verify the built tool list includes `read_skill_resource` and that its result identifies the selected skill and resource path.

- [x] **Step 2: Run the focused loop tests and confirm failure**

Run: `go test ./internal/runtime/loop -run 'Test.*SkillResource' -count=1`
Expected: FAIL because the tool is not registered.

- [x] **Step 3: Implement the tool and update descriptions**

Call `skills.LoadResource`, preserve per-tool loaded state, and return a bounded context wrapper consistent with `load_skill`.

- [x] **Step 4: Run the focused loop tests**

Run: `go test ./internal/runtime/loop -run 'Test.*SkillResource' -count=1`
Expected: PASS.

### Task 3: Make Stage-One Metadata Part of the Initial Prompt

**Files:**
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `README.md`

**Interfaces:**
- `buildSystemPrompt` includes the stage-one index and explains the three calls without requiring an index tool call.

- [x] **Step 1: Update prompt tests for progressive disclosure**

Assert that the prompt includes representative skill metadata, `load_skill`, `read_skill_resource`, and the three-stage instructions, while omitting the old instruction to call `load_skill` with `index` first.

- [x] **Step 2: Run the focused prompt tests and confirm failure**

Run: `go test ./internal/runtime/loop -run 'TestBuildSystemPrompt' -count=1`
Expected: FAIL against the old routing text.

- [x] **Step 3: Render `skills.Index()` into the initial prompt**

Keep the function signature stable, append the index as metadata, and include exact tool-selection guidance for stages two and three.

- [x] **Step 4: Document the three-stage behavior**

Describe startup metadata, on-demand skill bodies, and on-demand resource paths in the skill section of `README.md`.

- [x] **Step 5: Run focused tests and formatting**

Run: `gofmt -w skills/registry.go skills/registry_test.go internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop_test.go internal/runtime/loop/prompt.go internal/runtime/loop/prompt_test.go && go test ./skills ./internal/runtime/loop -count=1`
Expected: PASS.

### Task 4: Full Verification

**Files:**
- No additional files.

- [x] **Step 1: Run the complete verification suite**

Run: `go test ./... -race -count=1 && go build ./... && go vet ./... && git diff --check`
Expected: all commands exit 0.

- [x] **Step 2: Review the diff for scope**

Confirm only progressive skill loading, tests, plan, and README text changed on top of the existing worktree changes.
