# Session CLI Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support `pentgo`, `pentgo new`, `pentgo resume SESSION_ID`, and `pentgo delete SESSION_ID` from the current directory workspace.

**Architecture:** `cmd/pentgo` parses process arguments and delegates startup selection to `cli`. `app` owns validation of resumed sessions and the command lifecycle for deletion, while `storage` atomically removes session artifacts and updates the project index.

**Tech Stack:** Go 1.25+, standard library, existing application runtime.

## Global Constraints

- `pentgo` opens or creates `<cwd>/.pentgo/` and reuses the latest open session.
- `pentgo new` creates one new open session and enters the REPL focused on it.
- `pentgo resume SESSION_ID` opens only an existing open session and never creates a workspace.
- `pentgo delete SESSION_ID` removes the session artifact and transcript, then exits.
- Preserve shared evidence and blackboard data when deleting a session.

---

### Task 1: Delete A Stored Session

**Files:**
- Modify: `internal/adapters/storage/project_store.go`
- Test: `internal/adapters/storage/project_store_test.go`

**Interfaces:**
- Produces: `func (store *ProjectStore) DeleteSession(id string) error`.

- [x] **Step 1: Write the failing deletion test**

```go
func TestDeleteSessionRemovesArtifactAndProjectIndex(t *testing.T) {
	store := newTestStore(t)
	session := domain.NewSession("session-delete", "inspect", time.Now().UTC())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildProjectIndex(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.SessionDir(session.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory error = %v", err)
	}
}
```

- [x] **Step 2: Run the deletion test to verify it fails**

Run: `go test ./internal/adapters/storage -run TestDeleteSessionRemovesArtifactAndProjectIndex -count=1`
Expected: FAIL because `DeleteSession` is undefined.

- [x] **Step 3: Implement deletion with a rename rollback point**

Rename `sessions/<id>` to `tmp/delete-<id>` while holding the store commit mutex, remove the session summary from `project.json`, restore the directory if the index write fails, then remove the temporary directory and `resume/<id>`.

- [x] **Step 4: Run the deletion test to verify it passes**

Run: `go test ./internal/adapters/storage -run TestDeleteSessionRemovesArtifactAndProjectIndex -count=1`
Expected: PASS.

### Task 2: Expose Resume And Delete In App

**Files:**
- Modify: `internal/app/coordinator.go`
- Test: `internal/app/coordinator_test.go`

**Interfaces:**
- Produces: `ResumeSession(id string) (*domain.Session, error)`.
- Produces: `DeleteSession(id string) error`.

- [x] **Step 1: Write failing coordinator tests**

```go
func TestResumeAndDeleteSession(t *testing.T) {
	coordinator := New(config.Default(), t.TempDir(), Dependencies{})
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := coordinator.ResumeSession(session.ID); err != nil || resumed.ID != session.ID {
		t.Fatalf("resumed/err = %#v/%v", resumed, err)
	}
	if err := coordinator.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run the coordinator test to verify it fails**

Run: `go test ./internal/app -run TestResumeAndDeleteSession -count=1`
Expected: FAIL because the session command APIs are undefined.

- [x] **Step 3: Implement app-level validation and command deletion**

`ResumeSession` returns only an existing `domain.SessionOpen` snapshot. `DeleteSession` closes the command process runtime before delegating to `storage.ProjectStore.DeleteSession`.

- [x] **Step 4: Run the coordinator test to verify it passes**

Run: `go test ./internal/app -run TestResumeAndDeleteSession -count=1`
Expected: PASS.

### Task 3: Parse Process Commands And Select Startup Session

**Files:**
- Modify: `cmd/pentgo/main.go`
- Modify: `cmd/pentgo/main_test.go`
- Modify: `internal/cli/runtime_terminal.go`

**Interfaces:**
- Produces: `runCommand(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) int`.
- Produces: `cli.Startup{NewSession bool, ResumeSessionID string}`.

- [x] **Step 1: Write the failing command lifecycle test**

```go
func TestRunCommandNewResumeAndDeleteSession(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	if code := runCommand(context.Background(), []string{"new"}, strings.NewReader("/quit\n"), io.Discard, io.Discard); code != 0 {
		t.Fatalf("new exit code = %d", code)
	}
	// Load the only stored id, resume it, then delete it and verify the index is empty.
}
```

- [x] **Step 2: Run the command test to verify it fails**

Run: `go test ./cmd/pentgo -run TestRunCommandNewResumeAndDeleteSession -count=1`
Expected: FAIL because `runCommand` is undefined.

- [x] **Step 3: Parse commands and initialize CLI startup mode**

`main` passes `os.Args[1:]` to `runCommand`; `runREPL` delegates with no arguments. The parser accepts only the four documented forms, reports usage for invalid forms, and deletes without entering the REPL.

- [x] **Step 4: Run the command test to verify it passes**

Run: `go test ./cmd/pentgo -run TestRunCommandNewResumeAndDeleteSession -count=1`
Expected: PASS.

### Task 4: Document And Verify

**Files:**
- Modify: `README.md`

- [x] **Step 1: Replace command documentation with process command examples**

Document `pentgo`, `pentgo new`, `pentgo resume SESSION_ID`, and `pentgo delete SESSION_ID`.

- [x] **Step 2: Run verification**

Run: `go test ./... -count=1 && go test -race ./... && go vet ./... && go build -o /tmp/pentgo-session-command-check ./cmd/pentgo && go mod tidy -diff && git diff --check`
Expected: all commands exit 0.
