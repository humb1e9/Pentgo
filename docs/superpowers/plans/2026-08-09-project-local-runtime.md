# Project-Local Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each PentGo project self-contained in a directory, use the current working directory as the only project context for resume, and place runtime temporary files under that project's `tmp/` directory.

**Architecture:** The process working directory is the project boundary. A new project is created as a child directory of the current non-project directory; a startup inside a project directory opens that directory directly. Project metadata, blackboard, evidence, sessions, checkpoints, and reports remain below the project root, while MCP stdio processes inherit the project `tmp/` directory and project-scoped temporary environment variables.

**Tech Stack:** Go standard library, JSON/JSONL persistence, Eino checkpoint store, existing MCP stdio client.

## Global Constraints

- Keep the project data format file-based and dependency-free.
- Keep external MCP execution as the only Shell/Python execution path in PentGo.
- A project directory must contain `project.json` and be rejected when metadata is invalid.
- Startup from a project directory opens only that directory; it never selects a project from a global sibling scan.
- New project creation from inside an existing project is rejected.
- Preserve atomic writes and `0700` directories / `0600` files.

### Task 1: Define the project-local filesystem contract

**Files:**
- Modify: `internal/project/project.go`
- Modify: `internal/project/report.go`
- Modify: `internal/resume/store.go`
- Test: `internal/project/project_test.go`
- Test: `internal/resume/store_test.go`

- [ ] Change `project.Create(parentDir, name, now)` to create one child project directory with `sessions/`, `resume/`, and `tmp/` directories plus project metadata, blackboard, and evidence files.
- [ ] Change `project.Open(root)` to open one explicit project directory and add `ErrNotProject`/`IsProjectDir` for startup discovery.
- [ ] Add `SessionDir`, `ResumeDir`, `TmpDir`, and `IsProjectDir`; place each session transcript inside `sessions/<session-id>/transcript.jsonl`.
- [ ] Remove the `work/<session-id>` contract from `SessionArtifacts` and publish paths.
- [ ] Store resume snapshots under `resume/<session-id>/`.
- [ ] Add tests for the exact tree, current-directory opening, temporary directory, session transcript location, and resume location.

### Task 2: Make coordinator state current-directory scoped

**Files:**
- Modify: `internal/application/coordinator.go`
- Modify: `internal/config/config.go`
- Test: `internal/application/coordinator_test.go`

- [ ] Treat the constructor root as the current working directory rather than a global output root.
- [ ] Add `OpenCurrentProject(ctx)` and make it open the constructor root when it contains valid project metadata.
- [ ] Make `CreateProject` create a child project below the constructor root and reject creation when the constructor root is already a project.
- [ ] Remove coordinator project listing/open-by-ID behavior from runtime paths; session creation and persistence continue using the active project root.
- [ ] Connect the configured stdio MCP server with the active project's `tmp/` directory and expose `PENTGO_PROJECT_ROOT` / `PENTGO_PROJECT_TMP` to the child.
- [ ] Remove the unused global project output-root assumption from configuration and tests.

### Task 3: Change startup and terminal commands to local project semantics

**Files:**
- Modify: `cmd/pentgo/main.go`
- Modify: `internal/cli/project_terminal.go`
- Test: `cmd/pentgo/main_test.go`
- Test: `internal/cli/project_terminal_test.go`

- [ ] Start with `os.Getwd()` and call `OpenCurrentProject` before the REPL prompt.
- [ ] Restore the latest running session only from the opened current project.
- [ ] Make `/project new` and implicit first-session creation create a child directory from the current non-project directory.
- [ ] Make `/project open`, `/projects`, and project-ID resume commands report that the shell must be started from the target project directory; remove sibling project selection.
- [ ] Keep `/resume SESSION_ID` for sessions already loaded from the current project.
- [ ] Add tests proving startup in a project directory opens that project and startup in a plain directory does not select a sibling project.

### Task 4: Update documentation and verify the full behavior

**Files:**
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `.gitignore`

- [ ] Document `cd PROJECT_DIR && ../pentgo` as the resume workflow.
- [ ] Document the canonical project tree and project-local `tmp/` behavior.
- [ ] Remove documentation for global project scanning and `work/` paths.
- [ ] Ignore project-local `tmp/` contents without hiding project metadata, transcripts, evidence, or blackboard.
- [ ] Run focused tests, `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/...`, `go mod tidy -diff`, and `git diff --check`.

## Self-Review

- The requested project-local sessions and shared facts are covered by Task 1.
- The requested project-local temporary execution files are covered by Tasks 1 and 2.
- The requested “enter project directory before resume” rule is covered by Tasks 2 and 3.
- No global project registry, database, or new execution engine is introduced.
