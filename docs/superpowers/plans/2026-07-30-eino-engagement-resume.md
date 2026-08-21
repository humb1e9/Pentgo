# Eino Engagement Resume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resume an unfinished PentGo engagement after process restart using an engagement-local Eino ADK checkpoint, durable session snapshot, Journal, and work directory.

**Architecture:** Adopt CyberStrikeAI's useful core pattern, `CheckPointStore` plus a stable checkpoint ID and `Runner.Resume`, without its database, task manager, reconnect service, Web UI, or multi-agent runtime. An active engagement lives under `OUTPUT/.pentgo-active/ID/`; it is atomically promoted to the existing published `OUTPUT/ID/` only after a terminal session. MCP is reconnected once on resume, while the Journal and `work/` directory retain prior action evidence and files.

**Tech Stack:** Go 1.25, CloudWeGo Eino/ADK, current file-based JSONL Journal, standard-library JSON/filesystem primitives, current optional local stdio MCP SDK client.

## Global Constraints

- Keep one `ChatModelAgent`, one optional local stdio MCP server, and the existing four local tools.
- Use no database, HTTP/SSE transport, manager, reconnect loop, RBAC, monitoring, HITL, or multi-agent abstraction.
- Resume only sessions persisted as `running`; terminal engagements remain immutable published artifacts.
- Persist only under the private active directory: `checkpoint.bin`, `session.json`, `evidence.jsonl`, and `work/`.
- Preserve JSONL sequence numbers and successful-reference lookup across restart; do not rewrite prior evidence lines.
- MCP subprocesses are not checkpointed. Resume starts one fresh configured local subprocess before `Runner.Resume`.
- All tests use fake Eino models and local stdio fixture subprocesses.

---

### Task 1: Durable Active Engagement Store

**Files:**
- Create: `internal/runtime/resume/store.go`
- Create: `internal/runtime/resume/store_test.go`
- Modify: `internal/runtime/evidence/journal.go`
- Modify: `internal/runtime/evidence/journal_test.go`
- Modify: `internal/report/artifacts.go`
- Modify: `internal/report/artifacts_test.go`

**Interfaces:**
- Produce `resume.New(root, id) (*Store, error)`, `(*Store).SaveSession(*session.AgentSession) error`, `(*Store).LoadSession() (*session.AgentSession, error)`, `(*Store).CheckpointStore() compose.CheckPointStore`, `(*Store).Promote() (report.Artifacts, error)`, and `(*Store).Abort() error`.
- Produce `evidence.OpenJournal(path string, secrets ...string) (*Journal, error)` for an existing JSONL file.

- [ ] Write tests that create an active store, save/load a running compact session, reject path-traversal IDs, atomically promote a completed store, and remove an aborted store.
- [ ] Write Journal reopen tests with two existing records: the third append must get sequence 3, `Lookup(1)` must still return the successful record, and malformed existing JSONL must fail opening.
- [ ] Implement the store at `OUTPUT/.pentgo-active/ID` with `0700` directories and `0600` files. Use the existing `writeAtomic` pattern for session and checkpoint files.
- [ ] Implement `OpenJournal` by scanning existing JSONL, decoding every `Record`, rebuilding `records`, and setting `next` to the largest sequence. Open the file in append mode only after the scan succeeds.
- [ ] Refactor `EngagementWriter` into an active-or-new constructor that owns `work/`, `EvidencePath`, and atomic promotion from `.pentgo-active/ID` to `ID`; keep published output exactly `evidence.jsonl`, `session.json`, `report.md`, and `work/`.
- [ ] Run `go test ./internal/runtime/resume ./internal/runtime/evidence ./internal/report -count=1`.

### Task 2: Eino ADK Checkpoint And Session Snapshots

**Files:**
- Modify: `internal/runtime/loop/runner.go`
- Modify: `internal/runtime/loop/eino_run_loop.go`
- Modify: `internal/runtime/loop/eino_run_loop_test.go`

**Interfaces:**
- Add `RunnerConfig.CheckpointStore compose.CheckPointStore`, `CheckpointID string`, and `SaveSession func(*session.AgentSession) error`.
- Add `(*Runner).ResumeEino(context.Context, *session.AgentSession, model.ToolCallingChatModel) error`.

- [ ] Add a fake in-memory `compose.CheckPointStore` test that runs a tool-call turn, verifies `SaveSession` observes `Turns` and Journal-backed findings, then resumes through `adk.Runner.Resume` to a normal assistant final response.
- [ ] Add tests for a missing checkpoint (`resume checkpoint not found`), a corrupt checkpoint (`resume checkpoint error`), and a snapshot write failure. Each must fail the running session with a distinct stop reason and leave existing evidence unchanged.
- [ ] Build `adk.Runner` with `adk.RunnerConfig{Agent: agent, EnableStreaming: false, CheckPointStore: ...}` and start fresh runs with `adk.WithCheckPointID(CheckpointID)`.
- [ ] Implement `ResumeEino` with the same tool set and terminal event mapping as `RunEino`, but call `runner.Resume(ctx, CheckpointID)` rather than `Run`.
- [ ] Call `SaveSession` after every assistant event and after a successful `record_finding`; return its error through the ADK event path so the engagement becomes `failed/session_snapshot_error`.
- [ ] Run `go test ./internal/runtime/loop -count=1` and `go test -race ./internal/runtime/loop -count=1`.

### Task 3: Service Create, Resume, And Resource Ordering

**Files:**
- Modify: `internal/app/engagement.go`
- Modify: `internal/app/engagement_test.go`
- Create: `internal/app/engagement_resume_test.go`

**Interfaces:**
- Extend `app.Request` with `ResumeID string`; a non-empty value selects resume and ignores a new target/intent.
- Produce `Service.Run` paths for new and resumed active engagements.

- [ ] Add a local fake-model test that begins an engagement, yields after the first tool result, then constructs a second `Service` and resumes the same ID to a final response.
- [ ] Assert that the resumed model receives the pre-interruption tool result, evidence has one continuous sequence, `work/` preserves a file made before interruption, and the final published artifact has no `.pentgo-active/ID` directory.
- [ ] Add local stdio MCP resume coverage: the first process exits with the interrupted service; the resumed service starts one fresh fixture process and records its action with the next Journal sequence.
- [ ] Replace direct `report.NewEngagementWriter` construction with the active store. For new runs create the store and initial session snapshot before model execution; for resume load and require `SessionRunning`.
- [ ] Reconnect the optional MCP client before creating the Eino agent on both paths. Close resources in this order: MCP, Journal, Runtime scripts. Persist nonterminal cancellation/failure in the active store; promote only terminal normal, failed, or cancelled session results intended for final publication.
- [ ] Run `go test ./internal/app ./internal/runtime/loop -count=1` and `go test -race ./internal/app ./internal/runtime/loop -count=1`.

### Task 4: REPL Resume Command And Documentation

**Files:**
- Modify: `internal/terminal/terminal.go`
- Modify: `internal/terminal/terminal_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

- [ ] Add `/resume <engagement-id>` as an idle-only REPL command. It must reject blank IDs, run `app.Request{ResumeID: id, OutputRoot: terminal.outputRoot}`, and show the ordinary progress/result output.
- [ ] Add terminal tests for valid resume dispatch, blank-ID rejection, and rejection while another engagement is active.
- [ ] Document the active directory as crash-recovery state, state that `/resume ID` is the only recovery entry point, and describe the fresh MCP subprocess on resume.
- [ ] Document explicitly that no automatic retry/reconnect or cross-machine recovery is provided.
- [ ] Run `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`, and `go mod verify`.

### Task 5: Review And Commit

- [ ] Audit that active files never appear in published artifact directories and that terminal sessions cannot resume.
- [ ] Inspect `git diff --check` and run the complete verification commands from Task 4.
- [ ] Commit in two reviewable changes:

```bash
git commit -am "feat: persist active eino engagements"
git commit -am "feat: resume interrupted engagements"
```

## Design Decisions

- CyberStrikeAI's file checkpoint store is retained conceptually: atomic per-ID files, explicit checkpoint ID, and `Runner.Resume` with a controlled fallback. PentGo does not inherit its database trace replay, task manager, UI events, retry policy, or multi-agent state.
- The active directory is separate from staging and publication so a crash never loses `evidence.jsonl` through `EngagementWriter.Abort`, and incomplete state is never presented as a published engagement.
- The Journal is the durable source for completed actions; Eino's checkpoint is only continuation state. Both are required to give the resumed model coherent tool context and keep finding references valid.
