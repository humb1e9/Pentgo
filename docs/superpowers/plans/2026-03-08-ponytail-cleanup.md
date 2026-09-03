# Ponytail Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove the safest, evidence-backed accidental complexity identified by the whole-repository ponytail audit without changing runtime behavior or the explicit `*_test.go` ignore rule.

**Architecture:** Start with generated artifacts and unreferenced declarations, then make small local simplifications in existing packages. Preserve persistence, evidence redaction, path validation, MCP lifecycle, worker cancellation, and all current public behavior. Validate after each coherent batch and finish with full Go/build/static checks.

**Tech Stack:** Go 1.25, SQLite via modernc.org/sqlite, Eino ADK, Bubble Tea, Markdown/JSON documentation artifacts.

## Global Constraints

- Do not modify `.gitignore` rule `*_test.go`.
- Do not upload or re-add ignored `*_test.go` files.
- Do not remove cancellation, lifecycle cleanup, path validation, evidence redaction, transaction boundaries, or MCP resource closing.
- Preserve unrelated working-tree changes.
- Apply only read-only-audit-backed deletions and local shrinkage; no feature changes.

---

### Task 1: Remove disposable generated artifacts

**Files:**
- Delete: `docs/pentgo-runtime.workflow.html`
- Delete: `docs/pentgo-architecture.visual-check.json`

- [ ] **Step 1: Confirm both files are generated artifacts and unreferenced**

Run:
```bash
grep -R "pentgo-runtime.workflow.html\|pentgo-architecture.visual-check.json" -n --exclude-dir=.git . || true
```
Expected: no source or build script consumer.

- [ ] **Step 2: Delete the two artifacts**

Use the repository file deletion operation; do not replace them with new generators.

- [ ] **Step 3: Check the deletion diff**

Run:
```bash
git diff --stat -- docs/pentgo-runtime.workflow.html docs/pentgo-architecture.visual-check.json
git diff --check
```
Expected: only the two generated files are deleted and no whitespace errors appear.

---

### Task 2: Delete unreferenced declarations and helpers

**Files:**
- Modify: `internal/storage/fact_store.go`
- Modify: `internal/storage/helpers.go`
- Modify: `terminal/model.go`
- Test: existing package tests, without changing ignored test files

**Interfaces:**
- `storage.ProjectFactRepository` behavior remains unchanged.
- `terminal` layout continues using Go's built-in `max(int, int)`.

- [ ] **Step 1: Verify no callers**

Run:
```bash
grep -R "ErrFactNotFound\|nullableText\|max(" -n --include='*.go' .
```
Expected: `ErrFactNotFound` and `nullableText` have no callers; `max` references are call sites only.

- [ ] **Step 2: Remove the dead declarations and helper**

Delete `ErrFactNotFound`, delete `nullableText`, and delete the local `max` function. Do not alter `validID`, SQL NULL handling, or layout arithmetic.

- [ ] **Step 3: Run focused checks**

Run:
```bash
gofmt -w internal/storage/fact_store.go internal/storage/helpers.go terminal/model.go
go test ./internal/storage ./terminal -count=1
git diff --check
```
Expected: PASS.

---

### Task 3: Remove needless local tool parser flexibility

**Files:**
- Modify: `internal/tools/local.go`

**Interfaces:**
- `localTool.Invoke` still accepts JSON-decoded `[]any` arguments and returns the same validation errors.

- [ ] **Step 1: Remove only the unreachable direct `[]string` branch**

In `stringArguments`, retain nil/missing-key checks and the `[]any` string validation loop. Delete the `case []string` branch only.

- [ ] **Step 2: Format and test**

Run:
```bash
gofmt -w internal/tools/local.go
go test ./internal/tools -count=1
git diff --check
```
Expected: PASS.

---

### Task 4: Simplify safe MCP and skill code paths

**Files:**
- Modify: `internal/tools/mcp.go`
- Modify: `internal/tools/skill_catalog.go`

**Interfaces:**
- Keep `Connect`, `ConnectAll`, `ConnectStdio`, MCP transport validation, output bounds, and `Clients.Close` semantics.
- Keep skill frontmatter validation and Unicode-safe description normalization.

- [ ] **Step 1: Cache MCP transport selection**

At the start of `Connect`, assign `transportKind := cfg.Transport()` and use it in the switch, HTTP/SSE branch, and unsupported-transport error instead of calling `cfg.Transport()` repeatedly.

- [ ] **Step 2: Remove only duplicate skill description normalization if proven identical**

Inspect the scan/load paths. If Scan already stores the normalized description and Load revalidates required description, retain the Load validation and remove only the second normalization used solely for the same stored catalog value. Do not remove strict YAML parsing or Unicode truncation.

- [ ] **Step 3: Format and test**

Run:
```bash
gofmt -w internal/tools/mcp.go internal/tools/skill_catalog.go
go test ./internal/tools -count=1
git diff --check
```
Expected: PASS.

---

### Task 5: Simplify middleware construction and context plumbing only where behavior is unchanged

**Files:**
- Modify: `internal/agent/context_middleware.go`
- Modify: `internal/agent/turn_service.go`
- Modify: `internal/agent/evidence_middleware.go`

**Interfaces:**
- Preserve ADK middleware interfaces, context-window enforcement, facts loading, evidence redaction, and failure propagation.

- [ ] **Step 1: Inline the one-caller context middleware constructor**

Replace the sole `NewContextMiddleware(config)` call with `&ContextMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, config: config}` and delete the constructor. Keep `resolveFacts` unless inlining does not duplicate error handling.

- [ ] **Step 2: Check evidence middleware constructor return type before changing**

If `NewEvidenceMiddleware` is already consumed only as an ADK middleware and its concrete type satisfies the interface, return the concrete pointer type without changing behavior. Do not modify tests or redaction logic.

- [ ] **Step 3: Format and test**

Run:
```bash
gofmt -w internal/agent/context_middleware.go internal/agent/turn_service.go internal/agent/evidence_middleware.go
go test ./internal/agent ./internal/context -count=1
git diff --check
```
Expected: PASS.

---

### Task 6: Final repository validation and review

**Files:**
- Review all modified and deleted files.
- Preserve unrelated working-tree changes.

- [ ] **Step 1: Run full validation**

Run:
```bash
gofmt -w app cmd internal terminal
go test ./... -count=1
go build ./...
go vet ./...
go mod tidy -diff
go test -race ./internal/session ./internal/agent ./internal/storage -count=1
git diff --check
```
Expected: every command exits 0.

- [ ] **Step 2: Check residual symbols and empty files**

Run:
```bash
grep -R "ErrFactNotFound\|nullableText\|func max(" -n --include='*.go' . || true
python3 - <<'PY'
from pathlib import Path
for path in Path('.').rglob('*'):
    if path.is_file() and path.stat().st_size == 0:
        print(path)
PY
```
Expected: no residual symbols and no project empty files.

- [ ] **Step 3: Review status and ignored rules**

Run:
```bash
git status --short
git check-ignore -v internal/agent/manager_test.go
```
Expected: existing unrelated changes remain; `*_test.go` is still ignored.
