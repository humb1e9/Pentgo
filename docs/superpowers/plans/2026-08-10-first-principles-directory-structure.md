# First-Principles Directory Structure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize PentGo so every source directory represents exactly one stable responsibility without changing runtime behavior.

**Architecture:** Keep the executable composition in `cmd`, runtime use cases in `app`, provider-neutral agent types in `agent`, and pure project state in `domain`. Put every external implementation below `adapters`, while `cli` and `config` retain their literal meanings.

**Tech Stack:** Go 1.25+, Eino ADK, MCP Go SDK, standard Go tooling.

## Global Constraints

- Preserve all exported behavior, persisted file formats, CLI commands, and configuration keys.
- Add no compatibility packages or dependencies.
- Keep tests beside the package they verify.
- Leave the top-level `skills/` directory as runtime Markdown data.

---

### Task 1: Define Provider-Neutral Agent Types

**Files:**
- Move: `internal/contracts/tool.go` to `internal/agent/tool.go`
- Move: `internal/contracts/turn.go` to `internal/agent/turn.go`
- Modify: all Go imports and `contracts.` qualifiers

**Interfaces:**
- Produces: `agent.Tool`, `agent.ToolProvider`, `agent.Message`, `agent.TurnInput`, and `agent.ModelEngine`.

- [x] Move both files and change their package declaration to `agent`.
- [x] Replace `pentgo/internal/contracts` imports with `pentgo/internal/agent`.
- [x] Run `gofmt -w cmd internal`.

### Task 2: Consolidate Application Runtime

**Files:**
- Move: `internal/execution/*.go` to `internal/app/`
- Move: `internal/orchestrator/*.go` to `internal/app/`
- Modify: moved package declarations, imports, and package qualifiers

**Interfaces:**
- Produces: `app.Coordinator`, `app.TurnService`, `app.ProjectRuntime`, `app.SessionWorker`, and `app.Event`.

- [x] Move both source sets into `internal/app` and change their package declaration to `app`.
- [x] Remove former cross-package `execution.` qualifiers now that runtime and use-case code share one package.
- [x] Update CLI and executable imports from `orchestrator` and `execution` to `app`.

### Task 3: Group External Adapters

**Files:**
- Move: `internal/llm/*.go` to `internal/adapters/llm/`
- Move: `internal/mcp/*.go` to `internal/adapters/mcp/`
- Move: `internal/storage/*.go` to `internal/adapters/storage/`
- Move: `internal/skills/*.go` to `internal/adapters/skillfs/`

**Interfaces:**
- Produces: provider adapters under one namespace; `skillfs.Registry` remains the loader for top-level Markdown skill data.

- [x] Move adapter files without changing behavior.
- [x] Change only the skills adapter package declaration from `skills` to `skillfs`.
- [x] Update every adapter import path and qualifier.

### Task 4: Name The User Interface Literally

**Files:**
- Move: `internal/terminal/*.go` to `internal/cli/`
- Modify: `cmd/pentgo/main.go`
- Modify: `cmd/pentgo/main_test.go`

**Interfaces:**
- Produces: `cli.NewRuntimeTerminal` for the terminal user interface.

- [x] Rename package `terminal` to `cli`.
- [x] Update executable imports and qualifiers.
- [x] Run `go test ./... -count=1`.

### Task 5: Document And Verify The Result

**Files:**
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Documentation must match the directories present on disk and state one responsibility per directory.

- [x] Replace the old directory map and dependency diagram.
- [x] Confirm removed source paths have no references outside archived plans.
- [x] Run `go test ./... -count=1`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/...`, and `git diff --check`.
