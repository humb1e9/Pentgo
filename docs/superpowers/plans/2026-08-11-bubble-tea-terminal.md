# Bubble Tea Terminal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the line-oriented REPL with a usable Bubble Tea terminal interface while preserving current workspace, session, and command behavior.

**Architecture:** `app.Coordinator` exposes durable transcript messages for a focused session. `cli` owns a Bubble Tea model with session state, a viewport, a text input, and project event subscriptions; its existing startup modes continue to select the initial session. The executable and all persistence formats remain unchanged.

**Tech Stack:** Go 1.25+, Bubble Tea, Bubbles, Lip Gloss, existing app/runtime packages.

## Global Constraints

- Keep `pentgo`, `pentgo new`, `pentgo resume SESSION_ID`, and `pentgo delete SESSION_ID` behavior unchanged.
- Preserve every existing slash command and model/tool event path.
- Use Bubble Tea alternate-screen mode only for the actual stdin/stdout terminal pair.
- Do not add Markdown rendering, themes, mouse controls, or new configuration in this pass.

---

### Task 1: Expose Focused Transcript Messages

**Files:**
- Modify: `internal/app/coordinator.go`
- Test: `internal/app/coordinator_test.go`

**Interfaces:**
- Produces: `Messages(sessionID string) []agent.Message`.

- [x] **Step 1: Write a failing transcript retrieval test**

```go
messages := coordinator.Messages(session.ID)
if len(messages) != 2 || messages[0].Role != agent.RoleUser {
	t.Fatalf("messages = %#v", messages)
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app -run TestCoordinatorMessagesReturnsTranscript -count=1`
Expected: FAIL because `Messages` is undefined.

- [x] **Step 3: Return the runtime transcript snapshot through `Coordinator`**

Use `ProjectRuntime.Transcript(sessionID).Messages()` and return nil for an unopened session.

- [x] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/app -run TestCoordinatorMessagesReturnsTranscript -count=1`
Expected: PASS.

### Task 2: Add Bubble Tea And A Testable Terminal Model

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/cli/model.go`
- Test: `internal/cli/model_test.go`

**Interfaces:**
- Produces: a `terminalModel` implementing `tea.Model`.

- [x] **Step 1: Add a failing model rendering test**

```go
model := newTerminalModel(context.Background(), coordinator, session.ID)
model.width, model.height = 100, 32
if view := model.View(); !strings.Contains(view, session.ID) {
	t.Fatalf("view = %q", view)
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli -run TestTerminalModelRendersFocusedSession -count=1`
Expected: FAIL because `newTerminalModel` is undefined.

- [x] **Step 3: Add Bubble Tea dependencies and implement the model**

Use `bubbles/textinput` for prompt input, `bubbles/viewport` for conversation scrolling, and `lipgloss` for sidebar, header, status, and error styling. `Update` handles resize, text submission, slash commands, session selection, submitted-turn completion, and runtime events.

- [x] **Step 4: Run the model test to verify it passes**

Run: `go test ./internal/cli -run TestTerminalModelRendersFocusedSession -count=1`
Expected: PASS.

### Task 3: Run Bubble Tea From The Existing CLI Boundary

**Files:**
- Modify: `internal/cli/runtime_terminal.go`
- Delete: `internal/cli/input.go`
- Modify: `cmd/pentgo/main_test.go`

**Interfaces:**
- `RuntimeTerminal.Run` starts the Bubble Tea program after existing startup selection.

- [x] **Step 1: Keep the command lifecycle test as the regression check**

Run: `go test ./cmd/pentgo -run TestRunCommandNewResumeAndDeleteSession -count=1`
Expected: PASS before migration.

- [x] **Step 2: Replace the scanner loop with `tea.NewProgram`**

Use `tea.WithInput(terminal.input)` and `tea.WithOutput(terminal.output)` in all cases; append `tea.WithAltScreen()` only when those values are `os.Stdin` and `os.Stdout`.

- [x] **Step 3: Remove the obsolete scanner helper**

Delete `internal/cli/input.go` after `RuntimeTerminal` no longer calls `readLines`.

- [x] **Step 4: Run command lifecycle tests after migration**

Run: `go test ./cmd/pentgo -count=1`
Expected: PASS.

### Task 4: Document And Verify

**Files:**
- Modify: `README.md`

- [x] **Step 1: Describe the Bubble Tea interface and preserved slash commands**

- [x] **Step 2: Run verification**

Run: `go test ./... -count=1 && go test -race ./... && go vet ./... && go build -o /tmp/pentgo-bubbletea-check ./cmd/pentgo && go mod tidy -diff && git diff --check`
Expected: all commands exit 0.
