# Multi-Target Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow one PentGo session to accumulate multiple user-supplied HTTP(S) targets and authorize each target plus its subdomains.

**Architecture:** Preserve `AgentSession.Target` as the first target for existing reports and older project files. Add an ordered, deduplicated `Targets` collection that is persisted in session artifacts and project metadata. Before each turn, the coordinator extracts every URL or bare domain from the user message and appends new targets; the loop converts all session targets to allowed scope hosts.

**Tech Stack:** Go standard library and existing PentGo session, coordinator, project, loop, and authz packages.

## Global Constraints

- Keep all targets as synthetic local CTF fixtures.
- Preserve existing single-target project files and reports.
- Do not add dependencies or configuration fields.
- Only user-supplied URLs and domains extend a session target set.

---

### Task 1: Parse and Store Every User-Supplied Target

**Files:**
- Modify: `internal/runtime/session/target.go`
- Modify: `internal/runtime/session/session.go`
- Test: `internal/runtime/session/target_test.go`
- Test: `internal/runtime/session/session_test.go`

**Interfaces:**
- Produces: `ParseTargets(task string) []Target`, returning normalized unique targets in first-seen order.
- Produces: `(*AgentSession).AddTargets([]Target) bool`, appending normalized canonical values and setting `Target` only when it is empty.

- [x] **Step 1: Write failing parser and session tests**

```go
targets := ParseTargets("compare https://alpha.fixture.local and beta.fixture.local with https://alpha.fixture.local")
if got := []string{targets[0].Canonical, targets[1].Canonical}; !reflect.DeepEqual(got, []string{"https://alpha.fixture.local", "https://beta.fixture.local"}) {
	t.Fatalf("targets = %#v", targets)
}
```

```go
session := NewSession(Target{}, "hi", time.Now())
if !session.AddTargets(ParseTargets("https://alpha.fixture.local https://beta.fixture.local")) || session.Target != "https://alpha.fixture.local" || !reflect.DeepEqual(session.Targets, []string{"https://alpha.fixture.local", "https://beta.fixture.local"}) {
	t.Fatalf("session = %#v", session)
}
```

- [x] **Step 2: Run tests to verify failure**

Run: `go test ./internal/runtime/session -run 'TestParseTargets|TestSessionAddsTargets' -count=1`

Expected: FAIL because the parser and session target collection do not exist.

- [x] **Step 3: Implement parser and session collection**

```go
type AgentSession struct {
	// existing fields
	Targets []string `json:"targets,omitempty"`
}

func (session *AgentSession) AddTargets(targets []Target) bool {
	// Append each canonical target once, retaining input order.
}
```

- [x] **Step 4: Run focused session tests**

Run: `go test ./internal/runtime/session -run 'TestParseTargets|TestSessionAddsTargets' -count=1`

Expected: PASS.

### Task 2: Persist and Restore the Target Collection

**Files:**
- Modify: `internal/project/project.go:18-30,155-215`
- Modify: `internal/project/report.go:58-70`
- Modify: `internal/coordinator/coordinator.go:95-107,137-158`
- Test: `internal/project/project_test.go`

**Interfaces:**
- Consumes: `AgentSession.Targets []string`.
- Produces: `SessionIndex.Targets []string` and restored `AgentSession.Targets` values after reopening a project.

- [x] **Step 1: Write the failing project persistence assertion**

```go
agentSession.AddTargets([]session.Target{{Canonical: "https://api.fixture.local"}})
// After Open, metadata.Sessions[0].Targets contains both fixture URLs.
```

- [x] **Step 2: Run test to verify failure**

Run: `go test ./internal/project -run TestProjectCreateOpenAndList -count=1`

Expected: FAIL because `SessionIndex` has no persisted target collection.

- [x] **Step 3: Add JSON fields, cloning, restoration, and report output**

```go
Targets []string `json:"targets,omitempty"`
```

```go
agentSession := &sess.AgentSession{Target: entry.Target, Targets: append([]string(nil), entry.Targets...)}
```

- [x] **Step 4: Run project tests**

Run: `go test ./internal/project -count=1`

Expected: PASS.

### Task 3: Accumulate Targets Per Turn and Build a Multi-Host Scope

**Files:**
- Modify: `internal/coordinator/coordinator.go:137-158,373-391`
- Modify: `internal/runtime/loop/runner.go:130-136`
- Modify: `internal/runtime/loop/eino_turn.go:36`
- Modify: `internal/runtime/loop/eino_run_loop.go:26`
- Test: `internal/terminal/project_terminal_test.go`
- Test: `internal/runtime/loop/runner_test.go`

**Interfaces:**
- Consumes: `session.ParseTargets` and `AgentSession.AddTargets` before `RunTurn` constructs tools.
- Produces: an `authz.Scope` that accepts the primary target and every stored target host plus their subdomains.

- [x] **Step 1: Write failing multi-target terminal and scope tests**

```go
if err := terminal.handle(context.Background(), "compare https://beta.fixture.local and https://reference.fixture.local"); err != nil {
	t.Fatal(err)
}
waitForTurns(t, runtime, 2)
```

```go
scope := scopeForSession(session, nil, false)
if !scope.HostAllowed("api.beta.fixture.local") || scope.HostAllowed("outside.fixture") {
	t.Fatalf("scope did not represent session targets")
}
```

- [x] **Step 2: Run tests to verify failure**

Run: `go test ./internal/terminal ./internal/runtime/loop -run 'TestProjectTerminalAccumulatesTargetsInOneSession|TestScopeForSessionUsesAllTargets' -count=1`

Expected: FAIL because only the first target is kept in the session scope.

- [x] **Step 3: Bind all targets and build the shared scope**

```go
if runtime.session.AddTargets(sess.ParseTargets(message)) {
	if _, err := activeProject.PersistSession(runtime.session, coordinator.now()); err != nil {
		return err
	}
}
```

```go
func scopeForSession(session *sess.AgentSession, configured []string, allowPrivate bool) authz.Scope {
	// Add hostOf for every session.Targets entry to configured hosts.
}
```

- [x] **Step 4: Run focused packages**

Run: `go test ./internal/coordinator ./internal/terminal ./internal/runtime/loop -count=1`

Expected: PASS.

### Task 4: Document and Verify the User Workflow

**Files:**
- Modify: `README.md:129-164,294-312`
- Test: all packages

**Interfaces:**
- Consumes: the multi-target session behavior.
- Produces: documentation showing multiple targets in one session and durable target collection behavior.

- [x] **Step 1: Update the interaction example**

```text
pentgo> compare http://alpha.fixture.local with http://beta.fixture.local
pentgo> also inspect http://reference.fixture.local
```

- [x] **Step 2: Run the full suite and build the binary**

Run: `go test ./... -race -count=1 && go build -o bin/pentgo ./cmd/pentgo && git diff --check`

Expected: all packages pass, binary is rebuilt, and no whitespace errors are reported.
