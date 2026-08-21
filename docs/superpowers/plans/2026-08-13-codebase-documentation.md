# Codebase Documentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add maintainable documentation to every Go production module and annotate non-obvious test fixtures and concurrency cases.

**Architecture:** Documentation follows Go conventions: every package has a responsibility comment, exported API declarations explain contracts, and comments focus on ownership, side effects, persistence, concurrency, and protocol boundaries. Straightforward local variables and control flow remain self-documenting.

**Tech Stack:** Go 1.25, Eino ADK, MCP SDK, SQLite, Bubble Tea

## Global Constraints

- Preserve existing behavior and public API signatures.
- Use concise Chinese comments consistent with the existing Chinese-facing project documentation.
- Do not add comments that merely restate an identifier or a line of code.
- Format modified Go files with `gofmt` and validate with `go test ./... -count=1`, `go vet ./...`, and `git diff --check`.

---

### Task 1: Establish Documentation Coverage

**Files:**
- Modify: all production `*.go` files under `cmd/` and `internal/`
- Modify: test files only where setup, fixtures, cross-process behavior, or concurrency assertions require context

**Interfaces:**
- Consumes: current exported declarations and package boundaries
- Produces: package-level responsibility comments, exported API GoDoc, and comments for non-obvious internal invariants

- [ ] **Step 1: Inventory declarations and existing comments**

Run: `rg -n '^(type|func|const|var)' --glob '*.go' cmd internal`

Expected: a declaration inventory grouped by package.

- [ ] **Step 2: Define the documentation baseline**

Apply these rules to every module:

```text
Package comment: explain the package's runtime responsibility.
Exported declaration: explain its contract, ownership, or side effect.
Internal comment: explain an invariant, ordering constraint, or boundary that is not evident from the code.
Test comment: explain fixtures, subprocess protocol, race/concurrency expectations, or intentionally unusual data.
```

- [ ] **Step 3: Verify declaration coverage manually by package**

Run: `go doc ./...`

Expected: public packages and declarations render with meaningful documentation.

### Task 2: Document Core Agent Runtime

**Files:**
- Modify: `internal/agent/tool.go`
- Modify: `internal/agent/turn.go`
- Modify: `internal/domain/model.go`
- Modify: `internal/app/events.go`
- Modify: `internal/app/session_worker.go`
- Modify: `internal/app/project_runtime.go`
- Modify: `internal/app/turn_service.go`
- Modify: `internal/app/tools.go`
- Modify: `internal/app/coordinator.go`

**Interfaces:**
- Consumes: domain state transitions, the provider-neutral agent protocol, project resource ownership, and session event flow
- Produces: documentation for lifecycle, persistence ordering, tool evidence handling, and concurrency ownership

- [ ] **Step 1: Add package and exported API documentation**

Document the domain lifecycle, provider-neutral protocol, worker ownership model, project runtime resource ownership, and application services.

- [ ] **Step 2: Explain non-obvious runtime invariants**

Add targeted comments for worker-only mutation, bounded event delivery, snapshot publication, transcript-before-model ordering, and evidence persistence before model observation.

- [ ] **Step 3: Validate core packages**

Run: `go test ./internal/agent ./internal/domain ./internal/app -count=1`

Expected: PASS.

### Task 3: Document Adapter Boundaries

**Files:**
- Modify: `internal/adapters/builtins/workspace.go`
- Modify: `internal/adapters/llm/engine.go`
- Modify: `internal/adapters/llm/model_factory.go`
- Modify: `internal/adapters/llm/prompt.go`
- Modify: `internal/adapters/mcp/client.go`
- Modify: `internal/adapters/skillfs/registry.go`
- Modify: `internal/adapters/storage/evidence_store.go`
- Modify: `internal/adapters/storage/project_store.go`
- Modify: `internal/adapters/storage/sqlite.go`
- Modify: `internal/adapters/storage/transcript_store.go`

**Interfaces:**
- Consumes: adapter contracts from `internal/agent` and `internal/app`
- Produces: documentation for LLM conversion, MCP transport ownership, skill loading, workspace rooting, SQLite schema and transcript/evidence persistence

- [ ] **Step 1: Add package and exported API documentation**

Explain what each adapter owns and the contract it presents to the application layer.

- [ ] **Step 2: Explain protocol and durability boundaries**

Document schema conversion, MCP cleanup behavior, output bounds, redaction, transaction boundaries, migration ordering, transcript replay semantics, and workspace path anchoring.

- [ ] **Step 3: Validate adapters**

Run: `go test ./internal/adapters/... -count=1`

Expected: PASS.

### Task 4: Document User-Facing Composition

**Files:**
- Modify: `cmd/pentgo/main.go`
- Modify: `internal/cli/model.go`
- Modify: `internal/cli/runtime_terminal.go`
- Modify: `internal/config/config.go`
- Modify: relevant `*_test.go` fixture and concurrency sections

**Interfaces:**
- Consumes: CLI events, runtime dependencies, and persisted user configuration
- Produces: documentation for process composition, terminal state transitions, configuration normalization, and non-obvious tests

- [ ] **Step 1: Add package and exported API documentation**

Document process startup and shutdown, TUI model responsibilities, terminal lifecycle, and user-level configuration behavior.

- [ ] **Step 2: Annotate complex test setup**

Explain MCP subprocess fixtures, SQLite concurrency tests, event buffer tests, and model test doubles where their behavior is not clear from the test name.

- [ ] **Step 3: Validate CLI and entrypoint**

Run: `go test ./cmd/pentgo ./internal/cli ./internal/config -count=1`

Expected: PASS.

### Task 5: Format and Verify

**Files:**
- Modify: all Go files changed by Tasks 2-4

**Interfaces:**
- Consumes: completed documentation edits
- Produces: formatted, verified source tree

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w $(git diff --name-only -- '*.go')`

Expected: modified Go files use canonical formatting.

- [ ] **Step 2: Run complete verification**

Run: `go test ./... -count=1 && go vet ./... && git diff --check`

Expected: all commands exit with status 0.

- [ ] **Step 3: Review documentation diff**

Run: `git diff --check && git diff --stat`

Expected: the diff contains comments and no unintended behavior changes.
