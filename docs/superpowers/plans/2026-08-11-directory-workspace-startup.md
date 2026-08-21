# Directory Workspace Startup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Starting PentGo from any directory creates or reopens that directory's `.pentgo` workspace and focuses an open session.

**Architecture:** `storage` gains a direct-root constructor for `.pentgo`; the existing child-directory constructor remains for current callers. `app.Coordinator` owns workspace discovery, creation, and session selection, while `cli` only triggers that application workflow at startup.

**Tech Stack:** Go 1.25+, standard library, existing Eino and MCP integrations.

## Global Constraints

- Store all automatically created project data in `<cwd>/.pentgo/`.
- Create one `open` session on first startup; later startups reuse the most recently updated open session.
- Preserve the existing `/session new` command and existing project artifacts.
- Do not add dependencies, configuration, or a second persistence format.

---

### Task 1: Add Workspace-Level Storage Creation

**Files:**
- Modify: `internal/adapters/storage/project_store.go`
- Test: `internal/adapters/storage/project_store_test.go`

**Interfaces:**
- Produces: `CreateProjectStoreAt(root, name string, now time.Time) (*ProjectStore, error)`.
- Preserves: `CreateProjectStore(parent, name string, now time.Time) (*ProjectStore, error)` creates a `project-<id>` child.

- [x] **Step 1: Write the failing storage test**

```go
func TestCreateProjectStoreAtUsesExactRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".pentgo")
	store, err := CreateProjectStoreAt(root, "workspace", time.Now().UTC())
	if err != nil || store.Root() != root {
		t.Fatalf("store/err = %#v/%v", store, err)
	}
	if _, err := os.Stat(filepath.Join(root, "project.json")); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run the storage test to verify it fails**

Run: `go test ./internal/adapters/storage -run TestCreateProjectStoreAtUsesExactRoot -count=1`
Expected: FAIL because `CreateProjectStoreAt` is undefined.

- [x] **Step 3: Implement the direct-root constructor**

```go
func CreateProjectStoreAt(root, name string, now time.Time) (*ProjectStore, error) {
	return createProjectStore(root, name, newID("project"), now)
}
```

- [x] **Step 4: Run the storage test to verify it passes**

Run: `go test ./internal/adapters/storage -run TestCreateProjectStoreAtUsesExactRoot -count=1`
Expected: PASS.

### Task 2: Own Automatic Workspace And Session Selection In App

**Files:**
- Modify: `internal/app/coordinator.go`
- Test: `internal/app/coordinator_test.go`

**Interfaces:**
- Produces: `OpenOrCreateWorkspace(ctx context.Context) (*domain.Project, bool, error)`.
- Produces: `OpenOrCreateSession(intent string) (*domain.Session, bool, error)`.
- `bool` is true only when the project or session is newly created.

- [x] **Step 1: Write failing coordinator tests**

```go
func TestOpenOrCreateWorkspaceUsesHiddenDirectory(t *testing.T) {
	root := t.TempDir()
	coordinator := New(config.Default(), root, Dependencies{})
	_, created, err := coordinator.OpenOrCreateWorkspace(context.Background())
	if err != nil || !created {
		t.Fatalf("created/err = %v/%v", created, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".pentgo", "project.json")); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run the coordinator tests to verify they fail**

Run: `go test ./internal/app -run 'TestOpenOrCreateWorkspaceUsesHiddenDirectory|TestOpenOrCreateSession' -count=1`
Expected: FAIL because the startup APIs are undefined.

- [x] **Step 3: Implement workspace open/create and open-session reuse**

```go
const workspaceDirectory = ".pentgo"

func (coordinator *Coordinator) workspaceRoot() string {
	return filepath.Join(coordinator.root, workspaceDirectory)
}
```

`OpenOrCreateWorkspace` opens `<cwd>/.pentgo`, creates it with the cwd base name when absent, and still opens a legacy project when `<cwd>/project.json` exists. `OpenOrCreateSession` returns the most recently updated `domain.SessionOpen` session, otherwise creates one with intent `"交互会话"`.

- [x] **Step 4: Run the coordinator tests to verify they pass**

Run: `go test ./internal/app -run 'TestOpenOrCreateWorkspaceUsesHiddenDirectory|TestOpenOrCreateSession' -count=1`
Expected: PASS.

### Task 3: Initialize The Workspace In The CLI

**Files:**
- Modify: `internal/cli/runtime_terminal.go`
- Modify: `cmd/pentgo/main_test.go`
- Modify: `.gitignore`

**Interfaces:**
- `RuntimeTerminal.Run` invokes the two application startup APIs before reading input.

- [x] **Step 1: Replace the REPL startup test with a failing workspace test**

```go
func TestRunREPLInitializesWorkspaceAndSession(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	if code := runREPL(context.Background(), strings.NewReader("/quit\n"), io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".pentgo", "sessions")); err != nil {
		t.Fatal(err)
	}
}
```

- [x] **Step 2: Run the command test to verify it fails**

Run: `go test ./cmd/pentgo -run TestRunREPLInitializesWorkspaceAndSession -count=1`
Expected: FAIL because startup has not created `.pentgo`.

- [x] **Step 3: Initialize and focus a workspace session in `restoreCurrent`**

`restoreCurrent` calls `OpenOrCreateWorkspace`, then `OpenOrCreateSession("交互会话")`, sets `terminal.focused`, and begins event watching without submitting a model turn.

- [x] **Step 4: Ignore `.pentgo/` project data**

Add `/.pentgo/` to `.gitignore`.

- [x] **Step 5: Run the command test to verify it passes**

Run: `go test ./cmd/pentgo -run TestRunREPLInitializesWorkspaceAndSession -count=1`
Expected: PASS.

### Task 4: Update Startup Documentation And Verify

**Files:**
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

- [x] **Step 1: Document `<cwd>/.pentgo/` and startup session selection**

State that first startup creates a workspace and one session; later startup resumes the latest open session.

- [x] **Step 2: Run verification**

Run: `go test ./... -count=1 && go test -race ./... && go vet ./... && go build -o /tmp/pentgo-workspace-check ./cmd/pentgo && go mod tidy -diff && git diff --check`
Expected: all commands exit 0.
