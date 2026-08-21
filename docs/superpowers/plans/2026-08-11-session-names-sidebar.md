# Session Names Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every session a durable name and render the Bubble Tea sidebar as session name, complete session ID, and status.

**Architecture:** `domain.Session` owns `Name`; creation defaults it to the session ID and storage restores it from `session.json`, falling back to the legacy session ID. The CLI consumes the field and delegates rename requests to the application runtime.

**Tech Stack:** Go 1.25+, Bubble Tea, existing domain/storage/application packages.

## Global Constraints

- Persist the name as `name` in every new `session.json`.
- Load existing session artifacts without `name` by using the session ID.
- Render the full, untruncated session ID directly below the name.
- Support rename only through the TUI session command; do not add configuration or a migration command.

---

### Task 1: Persist Session Names With Legacy Fallback

**Files:**
- Modify: `internal/domain/model.go`
- Modify: `internal/adapters/storage/project_store.go`
- Modify: `internal/app/session_worker.go`
- Modify: `internal/app/project_runtime.go`
- Modify: `internal/app/coordinator.go`
- Test: `internal/domain/model_test.go`
- Test: `internal/adapters/storage/project_store_test.go`

**Interfaces:**
- Produces: `Session.Name string` with JSON key `name`.
- `NewSession(id, intent, startedAt)` uses the resolved session ID as `Name`.

- [x] **Step 1: Write failing domain and legacy storage tests**

```go
session := NewSession("session-1", "inspect TARGET", time.Now())
if session.Name != "session-1" {
	t.Fatalf("name = %q", session.Name)
}

legacy := sessionWire{ID: "session-legacy", Intent: "legacy task"}.session("session-legacy")
if legacy.Name != "session-legacy" {
	t.Fatalf("name = %q", legacy.Name)
}
```

- [x] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/domain ./internal/adapters/storage -run 'TestNewSessionDerivesName|TestLegacySessionDerivesName' -count=1`
Expected: FAIL because `Session.Name` is undefined.

- [x] **Step 3: Add the durable field and fallback normalization**

Add `Name` to `domain.Session` and `sessionWire`; trim persisted names during restoration, using the resolved session ID only when the persisted name is empty.

- [x] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/domain ./internal/adapters/storage -run 'TestNewSessionDerivesName|TestLegacySessionDerivesName' -count=1`
Expected: PASS.

### Task 2: Render Name And Complete ID In The Sidebar

**Files:**
- Modify: `internal/cli/model.go`
- Modify: `internal/cli/model_test.go`

**Interfaces:**
- Sidebar entry order: name, full `session.ID`, status and turn count. `/session rename NAME` updates the focused session.

- [x] **Step 1: Extend the failing model test**

```go
if !strings.Contains(view, session.Name) || !strings.Contains(view, session.ID) {
	t.Fatalf("view = %q", view)
}
```

- [x] **Step 2: Run the model test to verify it fails**

Run: `go test ./internal/cli -run TestTerminalModelRendersFocusedSession -count=1`
Expected: FAIL because the sidebar does not render the name.

- [x] **Step 3: Render the three-line sidebar entry**

Use the active style for the focused name, render `session.ID` without `shorten`, and retain the muted status line.

- [x] **Step 4: Run the model test to verify it passes**

Run: `go test ./internal/cli -run TestTerminalModelRendersFocusedSession -count=1`
Expected: PASS.

### Task 3: Verify

**Files:**
- No documentation changes required.

- [x] **Step 1: Run verification**

Run: `go test ./... -count=1 && go test -race ./... && go vet ./... && go build -o /tmp/pentgo-session-name-check ./cmd/pentgo && go mod tidy -diff && git diff --check`
Expected: all commands exit 0.
