# First Principles Directory Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize PentGo around explicit process, domain, application, adapter, and execution boundaries while preserving current behavior.

**Architecture:** `cmd/` remains limited to process entrypoints. The interactive host is composed from `application`, `cli`, `agent`, `mcp`, `execution/client`, `project`, `session`, `evidence`, and `resume`; the standalone execution process is composed from `execution/server`, `execution/engine`, `execution/policy`, and shared `execution` types. The vague `internal/runtime` umbrella and historical `internal/coordinator`, `internal/terminal`, and `internal/executionmcp` package locations are removed.

**Tech Stack:** Go 1.25+, Eino ADK, Model Context Protocol Go SDK, stdio MCP, Go test/race/vet/build.

## Global Constraints

- Preserve the existing MCP tool names: `run_execution`, `get_execution`, `wait_execution`, and `cancel_execution`.
- Preserve the two binaries: `cmd/pentgo` and `cmd/pentgo-execution`.
- Preserve transcript replay, ADK checkpoint resume, evidence journaling, authorization checks, and project persistence behavior.
- Do not add compatibility packages for the removed `internal/runtime` paths.
- Every moved package must have package and test names matching its new directory responsibility.

---

### Task 1: Establish The New Package Boundaries

**Files:**
- Move `internal/coordinator/*.go` to `internal/application/*.go` and rename package `coordinator` to `application`.
- Move `internal/terminal/*.go` to `internal/cli/*.go` and rename package `terminal` to `cli`.
- Move `internal/runtime/loop/*.go` to `internal/agent/loop/*.go` and keep package name `loop`.
- Move `internal/runtime/mcp/*.go` to `internal/mcp/*.go` and keep package name `mcp`.
- Create `internal/execution/client/` for the dedicated EXEC MCP client and move `ExecutionContext` plus its integration test there.
- Move `internal/runtime/resume/*.go` to `internal/resume/*.go` and keep package name `resume`.
- Move `internal/runtime/session/*.go` to `internal/session/*.go` and keep package name `session`.
- Move `internal/runtime/evidence/*.go` to `internal/evidence/*.go` and keep package name `evidence`.

**Interfaces:**
- `application.Coordinator` remains the host lifecycle API consumed by `cli` and `cmd/pentgo`.
- `loop.Runner`, `mcp.Client`, `resume.Store`, `session.AgentSession`, and `evidence.Journal` keep their existing public APIs.

- [x] Move files without altering behavior.
- [x] Update package declarations and imports to the new paths.
- [x] Run `gofmt -w` over moved Go files.
- [x] Run `go test ./internal/application ./internal/cli ./internal/agent/loop ./internal/mcp ./internal/resume ./internal/session ./internal/evidence` and confirm all packages pass.

### Task 2: Create The Execution Bounded Context

**Files:**
- Create `internal/execution/types.go` from `internal/runtime/exec/blocks.go` with package `execution` and the existing `Language` and `CodeBlock` types.
- Move `internal/runtime/exec/executor.go`, `preflight.go`, and `render.go` to `internal/execution/engine/` with package `engine`.
- Move `internal/runtime/exec/blocks_test.go` to `internal/execution/` as `types_test.go`.
- Move the remaining exec tests to `internal/execution/engine/` and rename package `exec` to `engine`.
- Move `internal/runtime/authz/*.go` to `internal/execution/policy/` and rename package `authz` to `policy`.
- Move `internal/executionmcp/*.go` to `internal/execution/server/` and rename package `executionmcp` to `server`.
- Update `cmd/pentgo-execution/main.go` to import `internal/execution/server`.

**Interfaces:**
- `execution.CodeBlock`, `execution.Language`, and execution result types are the shared execution contract.
- `execution/client.ConnectStdio` owns EXEC-specific schema hiding and `_pentgo_context` injection.
- `engine.Executor` consumes `execution.CodeBlock` through `engine.PreflightResult` and returns engine results.
- `policy.Authorizer.Authorize` consumes `execution.CodeBlock`.
- `server.ExecutionServerConfig` consumes `engine.Executor` and `policy.Authorizer`.

- [x] Move shared code-block types out of the implementation package.
- [x] Update engine, policy, server, and tests to use `execution`, `engine`, and `policy` imports.
- [x] Update all server configuration and MCP handler references.
- [x] Run `go test ./internal/execution/... ./cmd/pentgo-execution` and confirm the execution process still builds.

### Task 3: Rewire Host Dependencies

**Files:**
- Modify `cmd/pentgo/main.go` and `cmd/pentgo/main_test.go` to import `internal/application` and `internal/cli`.
- Modify `internal/application/*.go` to import the new `agent/loop`, `mcp`, `resume`, `session`, and `evidence` paths.
- Modify `internal/agent/loop/*.go` to import `project`, `session`, and `evidence` from their new paths.
- Modify `internal/project/*.go` and tests to import `internal/session`.
- Modify `internal/mcp/*.go` and tests to import `internal/evidence`.
- Modify all remaining tests and docs that reference `internal/runtime/*`, `internal/coordinator`, `internal/terminal`, or `internal/executionmcp`.

**Interfaces:**
- The application layer owns lifecycle wiring and remains the only host package that assembles project, MCP, model, Agent loop, resume, and evidence components.
- The CLI depends on application APIs and domain read models; it does not depend on execution engine internals.
- The execution server process depends on execution packages only and does not import application or CLI code.

- [x] Rewrite imports and aliases until `go list ./...` succeeds.
- [x] Run `go test ./...` and fix compile or package-cycle errors without adding compatibility wrappers.
- [x] Run `go vet ./...` and `go build ./cmd/...`.

### Task 4: Remove The Old Directory Tree And Document The New Shape

**Files:**
- Remove empty `internal/runtime/`, `internal/coordinator/`, `internal/terminal/`, and `internal/executionmcp/` directories after all files move.
- Modify `docs/ARCHITECTURE.md` to show the new tree and package ownership table.
- Modify `README.md` references to use `internal/application`, `internal/cli`, `internal/agent/loop`, `internal/mcp`, `internal/evidence`, `internal/session`, `internal/resume`, and `internal/execution/...`.

**Interfaces:**
- Documentation must describe the actual on-disk tree and the two process boundaries.
- No documentation may mention removed package paths or the old `runtime` umbrella.

- [x] Remove stale path references with `rg`.
- [x] Verify `internal/runtime` no longer exists on disk.
- [x] Run `git diff --check`.

### Task 5: Full Verification

**Files:**
- No source changes expected; only fixes discovered by verification are allowed.

- [x] Run `go test ./...`.
- [x] Run `go test -race ./...`.
- [x] Run `go vet ./...`.
- [x] Run `go build ./cmd/...`.
- [x] Run `go mod tidy -diff`.
- [x] Confirm `rg -n 'internal/runtime|internal/coordinator|internal/terminal|internal/executionmcp'` returns no matches outside archival plan material.
- [x] Confirm no executable or script artifacts were generated in the repository tree.
