# PentGo Agent Runtime Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild PentGo around explicit Project, Session, and Turn boundaries so concurrent sessions, durable transcripts, shared findings, and external tools have one clear owner each.

**Architecture:** A process owns one active `ProjectRuntime`; the runtime owns project resources and `SessionWorker` instances. Pure domain types sit below application services, while Eino, MCP, filesystem, and Skills live behind adapters. Transcript replay is the normal continuation mechanism; Eino checkpoint state is removed from the application recovery contract.

**Tech Stack:** Go 1.25, Eino ADK behind an adapter, MCP stdio behind a tool port, JSON/JSONL filesystem persistence, `sync` and `sync/atomic`, existing Go test tooling.

## Global Constraints

- Keep one active project per process; support multiple concurrent sessions inside that project.
- Keep `project.json`, `blackboard.json`, `evidence.jsonl`, and `sessions/<id>/` paths readable for existing projects.
- Use transcript replay as the durable recovery path; checkpoint files are not part of the application-level recovery contract.
- Keep MCP tool names, `evidence_ref` numbering, `/load_skill`, and `record_finding` contracts stable.
- Keep target scope attached to a session target set; shared facts and findings never widen another session scope.
- Preserve unrelated worktree changes and add no third-party dependencies.
- Keep all fixed system prompt prose in Chinese; technical protocol identifiers remain unchanged.
- Run focused tests after every task and finish with `go test -race ./...`, `go vet ./...`, and `git diff --check`.

## Target Package Layout

```text
internal/domain/                  pure project/session/turn/finding state
internal/contracts/              model, message, and tool contracts
internal/orchestrator/           project/session/turn use cases
internal/execution/              ProjectRuntime, SessionWorker, event fan-out
internal/storage/                project, transcript, evidence persistence
internal/llm/                    Eino model and tool-calling implementation
internal/mcp/                    MCP protocol and stdio process adapter
internal/skills/                 explicit filesystem skill registry
internal/terminal/               REPL parsing, focus, and event rendering
```

The implementation uses role-oriented package names. Each directory has one ownership boundary and the CLI uses only the new paths.

### Task 1: Introduce Pure Domain State

**Files:**
- Create: `internal/domain/model.go`
- Create: `internal/domain/model_test.go`
- Create: `internal/contracts/turn.go`
- Create: `internal/contracts/tool.go`

**Interfaces:**

```go
package domain

type SessionStatus string

const (
	SessionOpen      SessionStatus = "open"
	SessionCancelled SessionStatus = "cancelled"
	SessionFailed    SessionStatus = "failed"
	SessionClosed    SessionStatus = "closed"
)

type TurnStatus string

const (
	TurnRunning     TurnStatus = "running"
	TurnDone        TurnStatus = "done"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
)

type Finding struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id,omitempty"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Description    string `json:"description"`
	EvidenceRefs   []int  `json:"evidence_refs"`
	Recommendation string `json:"recommendation"`
}

type Session struct {
	ID            string        `json:"id"`
	Target        string        `json:"target"`
	Targets       []string      `json:"targets,omitempty"`
	Intent        string        `json:"intent"`
	Status        SessionStatus `json:"status"`
	Turns         int           `json:"turns"`
	ActiveTurnID  string        `json:"active_turn_id,omitempty"`
	FindingIDs    []string      `json:"finding_ids,omitempty"`
	FinalSummary  string        `json:"final_summary,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type Project struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Sessions   []SessionSummary `json:"sessions,omitempty"`
}

type SessionSummary struct {
	ID        string        `json:"id"`
	Status    SessionStatus `json:"status"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type Fact struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Source    string    `json:"source,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	At        time.Time `json:"at"`
}

type Blackboard struct {
	Facts    []Fact    `json:"facts"`
	Findings []Finding `json:"findings"`
}
```

```go
package contracts

type Message struct {
	Role         string
	Content      string
	ToolCallID   string
	ToolName     string
	ToolArguments map[string]any
}

type TurnInput struct {
	SessionID string
	Messages  []Message
	Tools     []Tool
}

type TurnEvent struct {
	Kind    string
	Message Message
	Tool    string
	Output  string
}

type ModelEngine interface {
	Run(context.Context, TurnInput) (<-chan TurnEvent, error)
}

type Tool interface {
	Name() string
	Description() string
	Invoke(context.Context, map[string]any) (string, error)
}

type ToolProvider interface {
	Tools(context.Context) ([]Tool, error)
}
```

- [x] Write `TestSessionRemainsOpenAfterTurnDone`, `TestFindingIDsAreUnique`, and `TestTurnStatusTransitions` against the domain package.
- [ ] Run `go test ./internal/domain ./internal/contracts -count=1`; the new tests initially fail because the packages and transitions are absent.
- [x] Implement the domain types, ID generation, target accumulation, finding validation, and explicit session/turn transitions.
- [x] Run `go test ./internal/domain ./internal/contracts -count=1` and require a passing result.

### Task 2: Build Canonical Filesystem Storage

**Files:**
- Create: `internal/storage/project_store.go`
- Create: `internal/storage/project_store_test.go`
- Create: `internal/storage/transcript_store.go`
- Create: `internal/storage/evidence_store.go`

**Interfaces:**

```go
type ProjectStore struct {
	root      string
	commitMu  sync.Mutex
}

type EvidenceStore struct {
	path string
	mu   sync.Mutex
}

func OpenProjectStore(root string) (*ProjectStore, error)
func CreateProjectStore(parent, name string, now time.Time) (*ProjectStore, error)
func (store *ProjectStore) LoadProject() (*domain.Project, error)
func (store *ProjectStore) LoadSession(id string) (*domain.Session, error)
func (store *ProjectStore) SaveSession(*domain.Session) error
func (store *ProjectStore) SaveProjectIndex(*domain.Project) error
func (store *ProjectStore) RebuildProjectIndex() error
func (store *ProjectStore) LoadBlackboard() (*domain.Blackboard, error)
func (store *ProjectStore) SaveBlackboard(*domain.Blackboard) error
func OpenEvidenceStore(path string) (*EvidenceStore, error)
func (store *EvidenceStore) Record(context.Context, string, map[string]any, bool, string) (int, error)
func (store *EvidenceStore) Lookup(int) (string, bool)
```

- [ ] Write `TestSessionArtifactWinsOverStaleIndex`, `TestResumeSnapshotIgnoredWithoutCheckpoint`, and `TestConcurrentProjectCommitsKeepEverySession`.
- [ ] Run `go test ./internal/storage -run 'TestSessionArtifactWinsOverStaleIndex|TestResumeSnapshotIgnoredWithoutCheckpoint|TestConcurrentProjectCommitsKeepEverySession' -count=1` and record the expected initial failures.
- [x] Make `sessions/<id>/session.json` authoritative for session state.
- [x] Treat `project.json` as a rebuildable session catalog containing IDs, status, and timestamps only.
- [x] Write the session artifact first, then publish the project index projection under `commitMu`.
- [x] Read old `findings` arrays from existing session files and convert them into project finding IDs during migration.
- [x] Make `resume/<id>/` a temporary compatibility directory. Load its snapshot only when `checkpoint.bin` exists and its session ID matches.
- [x] Rebuild the project index by scanning session artifact directories during open.
- [x] Keep transcript messages, evidence records, blackboard state, and reports in separate stores with explicit ownership.
- [x] Run `go test ./internal/storage -count=1`.

### Task 3: Replace Project and Session Runtime Ownership

**Files:**
- Create: `internal/execution/project_runtime.go`
- Create: `internal/execution/session_worker.go`
- Create: `internal/execution/session_worker_test.go`
- Create: `internal/execution/events.go`
- Modify: `internal/orchestrator/coordinator.go` to use the new runtime

**Interfaces:**

```go
type ProjectRuntime struct {
	project   *domain.Project
	store     *storage.ProjectStore
	blackboard *domain.Blackboard
	journal   *storage.EvidenceStore
	sessions  map[string]*SessionWorker
	ctx       context.Context
	cancel    context.CancelFunc
}

func OpenProjectRuntime(context.Context, *storage.ProjectStore, contracts.ToolProvider) (*ProjectRuntime, error)
func (runtime *ProjectRuntime) NewSession(string) (*domain.Session, error)
func (runtime *ProjectRuntime) Submit(context.Context, string, string) <-chan error
func (runtime *ProjectRuntime) Snapshot(string) *domain.Session
func (runtime *ProjectRuntime) Close() error
```

```go
type SessionWorker struct {
	// session is only mutated by the worker goroutine.
	session  *domain.Session
	snapshot atomic.Pointer[domain.Session]
	events   chan Event
}

type TurnFunc func(context.Context, *domain.Session, string) error

func NewSessionWorker(context.Context, *domain.Session, TurnFunc) (*SessionWorker, error)
func (worker *SessionWorker) Submit(context.Context, string) <-chan error
func (worker *SessionWorker) Events() <-chan Event
func (worker *SessionWorker) Snapshot() *domain.Session
func (worker *SessionWorker) Cancel(string) error
func (worker *SessionWorker) Stop()
```

`internal/execution/events.go` defines the event envelope used by the worker and CLI:

```go
type Event struct {
	SessionID string
	TurnID    string
	Kind      string
	Message   string
	Data      any
}
```

- [x] Write `TestSnapshotDoesNotWaitForLongTurn`, `TestProjectCloseStopsAllWorkers`, and `TestSessionUsesOriginalProjectRuntime`.
- [ ] Run `go test ./internal/execution -run 'TestSnapshotDoesNotWaitForLongTurn|TestProjectCloseStopsAllWorkers|TestSessionUsesOriginalProjectRuntime' -count=1` and verify the initial failures.
- [x] Make the worker goroutine the sole owner of mutable session state.
- [x] Publish cloned snapshots at turn start, tool completion, finding creation, turn completion, cancellation, and failure.
- [x] Make `ProjectRuntime` the owner of all session workers, MCP tools, journal, blackboard, and cancellation.
- [x] Derive runtime context from the caller context and serialize open/create/close with a private lifecycle mutex and centralized close path.
- [x] Emit typed session events through a bounded per-session channel; retain the latest snapshot for `/status` and `/sessions`.
- [x] Keep project index and blackboard as separately atomic documents; recover either projection from canonical session and blackboard documents after a partial process interruption.
- [x] Run `go test ./internal/execution ./internal/orchestrator -count=1`.

### Task 4: Implement the Application Turn Service

**Files:**
- Create: `internal/orchestrator/turn_service.go`
- Create: `internal/orchestrator/turn_service_test.go`
- Modify: `internal/orchestrator/coordinator.go`
- Modify: `internal/terminal/runtime_terminal.go`

**Interfaces:**

```go
type TurnService struct {
	engine contracts.ModelEngine
	store  *storage.ProjectStore
}

func (service *TurnService) RunTurn(
	context.Context,
	*execution.ProjectRuntime,
	*domain.Session,
	string,
) error
```

- [x] Write `TestTurnPersistsUserToolAssistantMessages`, `TestTurnDoneLeavesSessionOpen`, and `TestCancelledTurnLeavesDurableEvidence`.
- [ ] Run `go test ./internal/orchestrator -run 'TestTurnPersistsUserToolAssistantMessages|TestTurnDoneLeavesSessionOpen|TestCancelledTurnLeavesDurableEvidence' -count=1` and verify the initial failures.
- [x] Append the user message before model execution.
- [x] Persist every tool call and tool result in transcript order; persist evidence before exposing a successful tool result to the model.
- [x] Set `TurnDone` and update the session snapshot after the final assistant message.
- [x] Keep the session open after a completed turn.
- [x] On restart, load the last complete transcript and create a fresh model run; historical tool calls are replayed as messages and never invoked again.
- [x] Route all normal user messages and explicit continuation commands through this service.
- [x] Run `go test ./internal/orchestrator ./internal/terminal -count=1`.

### Task 5: Move Eino Behind an Adapter

**Files:**
- Create: `internal/llm/engine.go`
- Create: `internal/llm/engine_test.go`
- Create: `internal/llm/prompt.go`
- Create: `internal/llm/model_factory.go`
- Move: `internal/agent/eino_model.go` provider construction into `internal/llm/model_factory.go`
- Move: `internal/agent/types.go` provider DTO helpers into `internal/llm/model_factory.go`
- Remove after migration: `internal/agent/loop/eino_agent.go`
- Remove after migration: `internal/agent/loop/eino_turn.go`
- Remove after migration: `internal/agent/loop/eino_run_loop.go`
- Remove after migration: `internal/agent/model_factory.go`

**Interfaces:**

```go
type Engine struct {
	model model.ToolCallingChatModel
	tools []contracts.Tool
}

func NewEngine(context.Context, model.ToolCallingChatModel, []contracts.Tool) (*Engine, error)
func (engine *Engine) Run(context.Context, contracts.TurnInput) (<-chan contracts.TurnEvent, error)
```

- [x] Write `TestEinoEngineMapsToolCallAndResult`, `TestEinoEngineUsesChineseSystemPrompt`, and `TestEinoEngineDoesNotPersistDomainState`.
- [ ] Run `go test ./internal/llm -count=1` and verify the initial failures.
- [x] Move prompt construction and Eino tool schema generation into the adapter.
- [x] Convert Eino messages to `contracts.Message` values at the adapter boundary.
- [x] Keep session, project, blackboard, and evidence state out of the new Eino adapter configuration; the old loop is removed after adapter migration.
- [x] Keep all state mutation and persistence in `TurnService` and `ProjectRuntime`.
- [x] Run `go test ./internal/llm ./internal/orchestrator -count=1`.

### Task 6: Move MCP and Skills Behind Explicit Adapters

**Files:**
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/client_test.go`
- Create: `internal/skills/registry.go`
- Create: `internal/skills/registry_test.go`

- [x] Write `TestMCPAdapterReturnsPortsTool`, `TestMCPToolEvidenceIsRecordedByRuntime`, and `TestSkillRegistryUsesInjectedFS`.
- [ ] Run the focused tests and verify failures before moving implementation.
- [x] Make MCP return `contracts.Tool` descriptors instead of Eino `tool.BaseTool` values.
- [x] Keep evidence recording in the runtime decorator around tool invocation.
- [x] Make the Skills Registry accept an explicit `fs.FS`; remove current-working-directory discovery and the package-level default registry.
- [x] Preserve `/load_skill` and exact skill names while making each ProjectRuntime own its registry instance.
- [x] Run `go test ./internal/mcp ./internal/skills ./internal/execution -count=1`.

### Task 7: Migrate CLI and Remove Transitional Runtime Paths

**Files:**
- Modify: `cmd/pentgo/main.go`
- Modify: `internal/terminal/runtime_terminal.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `.gitignore` to track `docs/superpowers/plans/*.md`
- [x] Remove old compatibility paths: `internal/runtime/application/`, `internal/runtime/project/`, `internal/runtime/session/`, `internal/runtime/resume/`, `internal/agent/`, `internal/mcp/`, `internal/runtime/evidence/`, `internal/cli/project_terminal.go`, and `skills/registry.go`.

- [x] Update CLI commands to target the one active ProjectRuntime and subscribe to session events.
- [x] Keep `/session new`, `/session switch`, `/session list`, `/session cancel`, `/blackboard`, `/status`, and `/load_skill` behavior.
- [x] Remove stale product terminology and document the authoritative files and transcript-first continuation model.
- [x] Add a tracked plan-file exception under `/docs/superpowers/plans/`.
- [x] Run `go test ./cmd/pentgo ./internal/terminal -count=1`.

### Task 8: Full Verification

**Files:**
- Verify all changed Go and Markdown files.

- [x] Run `gofmt -w` on every changed Go file.
- [x] Run `go test ./... -count=1`.
- [x] Run `go test -race ./...`.
- [x] Run `go vet ./...`.
- [x] Run `git diff --check`.
- [x] Run `git status --short`; preserve unrelated existing worktree changes and remove the old compatibility paths after replacement tests pass.
