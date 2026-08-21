# Optional Initial Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the first non-empty terminal message create a project and session even when it has no HTTP(S) target.

**Architecture:** `Coordinator.NewSession` will retain an empty target when none is present in the initial task. Before each turn, `Coordinator.runTurn` will bind and persist the first target discovered while the session target is empty; the loop then constructs its authorization scope from that target. The terminal will name a newly created project from the first message instead of requiring a parsed URL.

**Tech Stack:** Go standard library, existing PentGo coordinator, terminal, session, and test packages.

## Global Constraints

- Keep all targets as synthetic local CTF fixtures.
- Do not add dependencies or new configuration.
- A target, once bound, remains unchanged for the lifetime of a session.

---

### Task 1: Make Session Targets Optional Until First URL

**Files:**
- Modify: `internal/coordinator/coordinator.go:137-158,373-391`
- Test: `internal/terminal/project_terminal_test.go`

**Interfaces:**
- Consumes: `session.ParseTarget(task string) (session.Target, error)`.
- Produces: `Coordinator.NewSession(task string) (*session.AgentSession, error)` with an empty `Target` when `task` has no HTTP(S) URL or bare domain.

- [x] **Step 1: Write the failing terminal behavior test**

```go
if err := terminal.handle(context.Background(), "hi"); err != nil {
	t.Fatal(err)
}
waitForTurns(t, runtime, 1)
if sessions := runtime.Sessions(); len(sessions) != 1 || sessions[0].Target != "" {
	t.Fatalf("sessions = %#v", sessions)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/terminal -run TestProjectTerminalStartsWithoutTargetThenBindsFirstTarget -count=1`

Expected: FAIL with `task does not contain an HTTP(S) target`.

- [x] **Step 3: Implement optional initial target and first-target binding**

```go
target, _ := sess.ParseTarget(task)
agentSession := sess.NewSession(target, strings.TrimSpace(task), coordinator.now())
```

```go
if runtime.session.Target == "" {
	if target, err := sess.ParseTarget(message); err == nil {
		runtime.session.Target = target.Canonical
		if _, err := activeProject.PersistSession(runtime.session, coordinator.now()); err != nil {
			return err
		}
	}
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `go test ./internal/terminal -run TestProjectTerminalStartsWithoutTargetThenBindsFirstTarget -count=1`

Expected: PASS.

### Task 2: Remove Target-First Terminal Copy and Update Documentation

**Files:**
- Modify: `internal/terminal/project_terminal.go:110,191-200,212-224,338-345`
- Modify: `internal/runtime/loop/prompt.go:11-21`
- Modify: `README.md:129-164,363-370`

**Interfaces:**
- Consumes: `Coordinator.CreateProject(ctx context.Context, name string)` and `Coordinator.NewSession(task string)`.
- Produces: a project named after the first message and clear UI guidance that a URL can be supplied after the session starts.

- [x] **Step 1: Replace the terminal project-creation branch**

```go
if !terminal.coordinator.HasProject() {
	metadata, err := terminal.coordinator.CreateProject(ctx, task)
	if err != nil {
		return err
	}
	terminal.write("project opened: " + metadata.ID + "\n")
}
```

- [x] **Step 2: Change user-facing copy and model instruction**

```text
Enter a message to start, then chat normally.
```

```text
A target may be absent initially. Do not use network tools until the user supplies a target; once supplied, keep work within that target's scope.
```

- [x] **Step 3: Update README examples and command descriptions**

```text
pentgo> hi
pentgo> inspect http://fixture.local and record the observable evidence
```

- [x] **Step 4: Run terminal and prompt tests**

Run: `go test ./internal/terminal ./internal/runtime/loop -count=1`

Expected: PASS.

### Task 3: Verify the Repository

**Files:**
- Test: `internal/coordinator/coordinator_test.go`
- Test: `internal/terminal/project_terminal_test.go`

**Interfaces:**
- Consumes: the updated terminal and coordinator behavior.
- Produces: regression coverage for opening without a target, binding the first target, and retaining scope thereafter.

- [x] **Step 1: Run focused package tests**

Run: `go test ./internal/coordinator ./internal/terminal ./internal/runtime/loop -count=1`

Expected: PASS.

- [x] **Step 2: Run the full suite and race detector**

Run: `go test ./... -race -count=1`

Expected: PASS.

- [x] **Step 3: Inspect the final diff**

Run: `git diff --check && git diff -- internal/coordinator/coordinator.go internal/terminal/project_terminal.go internal/terminal/project_terminal_test.go internal/runtime/loop/prompt.go README.md`

Expected: no whitespace errors; only optional-first-target behavior, tests, and documentation changes.
