# Project Runtime Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make project/session state durable and coherent under concurrent turns, project reopen, cancellation, and checkpoint continuation.

**Architecture:** Keep the current package layout for this pass, but make `Coordinator` the lifecycle boundary for one active project and bind every session runtime to the project handle it was created under. Serialize project persistence, treat the session artifact as the recovery source, and make normal and resumed turns share the same turn-level lifecycle semantics.

**Tech Stack:** Go 1.25, Eino ADK, JSON/JSONL filesystem persistence, existing `sync` primitives and Go tests.

## Global Constraints

- Preserve the existing MCP tool names, evidence references, skill loading contract, and on-disk paths.
- Preserve user-created worktree changes; touch only files required by this plan and related tests.
- Keep a turn completion separate from `AgentSession` completion; a final assistant message returns the session to an idle/runnable state.
- Run focused tests after each implementation step and `go test -race ./...` plus `go vet ./...` before completion.

### Task 1: Serialize Project Snapshots

**Files:**
- Modify: `internal/runtime/project/project.go:43-259`
- Modify: `internal/runtime/project/blackboard.go:23-153`
- Test: `internal/runtime/project/project_test.go`
- Test: `internal/runtime/project/blackboard_test.go`

**Interfaces:**
- `Project.Save()` remains the public commit operation.
- `Blackboard.Save()` remains safe when called concurrently.

- [ ] Add a project-level save mutex that covers metadata snapshot, metadata publication, and blackboard publication.
- [ ] Add a blackboard-level save mutex so direct blackboard saves cannot publish stale snapshots out of order.
- [ ] Add concurrent persistence coverage that creates multiple sessions/facts, waits for all saves, reopens the project, and checks every entry is present.
- [ ] Run `go test ./internal/runtime/project -count=1`.

### Task 2: Bind Session Runtime to Its Project

**Files:**
- Modify: `internal/runtime/application/coordinator.go:24-38`
- Modify: `internal/runtime/application/project.go:12-143`
- Modify: `internal/runtime/application/session.go:86-149`
- Modify: `internal/runtime/application/turn.go:13-108`
- Modify: `internal/runtime/project/project_runtime.go:50-73`
- Test: `internal/runtime/application/coordinator_test.go`

**Interfaces:**
- `sessionRuntime` stores the `*project.Runtime` captured during session creation.
- Turn execution reads that captured runtime instead of resolving `Coordinator.projectRuntime` again.

- [ ] Add lifecycle serialization for create/open/close operations so a new project cannot become current while an old project is draining.
- [ ] Capture the current project runtime in `addSession`.
- [ ] Use the captured runtime for journal, MCP, project, and blackboard access in normal and resumed turns.
- [ ] Derive the project runtime context from the caller context rather than `context.Background()`.
- [ ] Add a test that closes/reopens project state while a session object is still present and verifies the session keeps its original project handle.
- [ ] Run `go test ./internal/runtime/application ./internal/runtime/project -count=1`.

### Task 3: Unify Resume Turn Semantics

**Files:**
- Modify: `internal/agent/loop/eino_run_loop.go:22-133`
- Modify: `internal/agent/loop/eino_turn.go:19-96`
- Test: `internal/agent/loop/eino_run_loop_test.go`
- Test: `internal/runtime/application/coordinator_test.go`

**Interfaces:**
- `ResumeEino` keeps its existing signature.
- A final assistant response increments the turn and updates `FinalSummary`; it does not close the session.

- [ ] Inject `buildProjectSystemPrompt` into resumed agents so blackboard context is present on both paths.
- [ ] Align resumed message counting, cancellation, maximum-iteration, and empty-response behavior with `RunTurn`.
- [ ] Add a regression test for resumed execution retaining blackboard context and `SessionRunning` status after a final response.
- [ ] Run `go test ./internal/agent/loop ./internal/runtime/application -count=1`.

### Task 4: Make Session and Finding Persistence Consistent

**Files:**
- Modify: `internal/runtime/project/project.go:158-202`
- Modify: `internal/runtime/application/project.go:40-65`
- Modify: `internal/agent/loop/eino_agent.go:58-103`
- Test: `internal/runtime/project/project_test.go`
- Test: `internal/runtime/application/coordinator_test.go`
- Test: `internal/agent/loop/eino_run_loop_test.go`

**Interfaces:**
- Add `Project.LoadSession(id string) (*session.AgentSession, error)` for reopening the authoritative session artifact.
- Existing `PersistSession` continues returning `SessionArtifacts`.

- [ ] Load session state from `sessions/<id>/session.json` during project reopen, with the project index as a compatibility fallback.
- [ ] Check session-level duplicate finding titles before mutating the project blackboard, preventing a partial duplicate write.
- [ ] Add tests for artifact-first reopen and duplicate finding consistency.
- [ ] Run `go test ./internal/runtime/project ./internal/runtime/application ./internal/agent/loop -count=1`.

### Task 5: Expose Shared Findings in Model Context

**Files:**
- Modify: `internal/runtime/project/blackboard.go:101-133`
- Modify: `internal/agent/loop/prompt.go` only if the new snapshot labels require it
- Test: `internal/runtime/project/blackboard_test.go`
- Test: `internal/agent/loop/eino_run_loop_test.go`

**Interfaces:**
- `Blackboard.Snapshot(maxFacts int)` remains the model-facing text API.

- [ ] Include bounded project findings in the snapshot alongside facts.
- [ ] Preserve the existing empty-facts output contract where tests rely on it.
- [ ] Add a test proving `read_blackboard` returns a recorded finding.
- [ ] Run `go test ./internal/runtime/project ./internal/agent/loop -count=1`.

### Task 6: Full Verification and Documentation Alignment

**Files:**
- Review and modify only as needed: `README.md`, `docs/ARCHITECTURE.md`, `internal/agent/loop/eino_agent.go`

- [ ] Remove stale lifecycle/product terminology that contradicts the active project/session/turn model.
- [ ] Update architecture documentation to state the single active project boundary and the authoritative persistence files.
- [ ] Run `gofmt -w` on changed Go files.
- [ ] Run `go test -race ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `git diff --check` and inspect the final status without reverting unrelated worktree changes.
