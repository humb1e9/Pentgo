# Conversation Terminology Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the runtime-facing `transcript` name with `conversation` while preserving the existing SQLite schema and stored data compatibility.

**Architecture:** Rename the Go store, ProjectStore/ProjectRuntime APIs, local fields, helpers, tests, and current runtime documentation from `Transcript` to `Conversation`. Keep SQLite table names `transcript_messages` and `transcript_tool_calls` unchanged because they are persisted schema identifiers, not runtime-facing concepts; their SQL migration compatibility must remain intact.

**Tech Stack:** Go 1.25, SQLite, Go standard-library testing.

## Global Constraints

- Rename all active runtime-facing Go identifiers and current product documentation from `transcript` to `conversation`.
- Preserve `transcript_messages` and `transcript_tool_calls` SQLite table names and their existing schema/migration behavior.
- Do not rename archived historical implementation plans under `docs/superpowers/plans/`.
- Do not add a compatibility alias for the old Go APIs; this is an internal all-call-site rename.
- Preserve message ordering, append atomicity, evidence ordering, context-surface behavior, and session recovery behavior.

---

## File Structure

| File group | Responsibility after rename |
| --- | --- |
| `internal/project/conversation_store.go`, `conversation_store_test.go` | Persisted, ordered, model-visible session message history and its direct tests. |
| `internal/project/project_store.go`, `internal/bootstrap/project_runtime.go`, `internal/project/turn/runtime.go`, `internal/project/turn/service.go` | Open, own, expose, append, and replay a session conversation. |
| `internal/project/context/*.go` | Read the immutable raw conversation as the source for Context Surface materialization and compaction. |
| `internal/bootstrap/*.go`, `internal/terminal/*.go`, `internal/model/*.go` | Refer to persisted conversation consistently in orchestration, UI comments/tests, and model instructions. |
| `README.md`, `docs/TECHNICAL.md`, `docs/ARCHITECTURE.md` | Describe the persisted message history as a conversation while documenting legacy SQLite table names explicitly where relevant. |

### Task 1: Rename the persisted store API and direct tests

**Files:**
- Move: `internal/project/transcript_store.go` → `internal/project/conversation_store.go`
- Move: `internal/project/transcript_store_test.go` → `internal/project/conversation_store_test.go`
- Modify: `internal/project/project_store.go`
- Modify: `internal/project/sqlite.go`
- Modify: `internal/project/sqlite_test.go`
- Modify: `internal/project/project_store_test.go`
- Modify: `internal/project/project_fact_store_test.go`

**Interfaces:**
- Consumes: existing `ProjectStore`, `core.Message`, and SQLite `transcript_messages` / `transcript_tool_calls` tables.
- Produces: `type ConversationStore`, `func (store *ProjectStore) OpenConversation(id string) (*ConversationStore, error)`, and private `loadConversationDB` / `loadConversationQueryer` helpers.

- [ ] **Step 1: Rename the store file and replace its Go-level API names**

Move the file and use these exact public declarations; retain SQL table names unchanged:

```go
// ConversationStore 以严格顺序追加一个会话的模型可见消息历史，
// 并缓存副本以降低后续 turn 回放开销。
type ConversationStore struct {
    // retain the existing fields and behavior
}

// OpenConversation 打开独立的 SQLite 连接，并加载一个有效会话的完整有序消息历史。
func (store *ProjectStore) OpenConversation(id string) (*ConversationStore, error)
```

Rename `TranscriptStore` to `ConversationStore`, `OpenTranscript` to `OpenConversation`, `loadTranscriptDB` to `loadConversationDB`, `transcriptQueryer` to `conversationQueryer`, and `loadTranscriptQueryer` to `loadConversationQueryer`. Change all Go error/comment wording from `transcript` to `conversation`, but keep all `INSERT INTO transcript_messages`, `FROM transcript_messages`, and `transcript_tool_calls` SQL intact.

- [ ] **Step 2: Rename the direct store tests and update their target symbols**

Move `transcript_store_test.go` to `conversation_store_test.go`. Rename tests to:

```go
func TestConversationStoreRoundTripsNormalizedMessages(t *testing.T)
func TestConversationStoresAllocateSequencesAcrossHandles(t *testing.T)
func TestConversationStoreAppendBatchIsAtomic(t *testing.T)
func TestConversationMessagesReturnsDeepCopy(t *testing.T)
```

Replace `OpenTranscript`, `TranscriptStore`, and `loadTranscriptDB` references with their new names. Keep fixture session IDs, database table queries, and test behavior otherwise unchanged.

- [ ] **Step 3: Update project-level callers without changing schema assertions**

Across the listed project tests and source, replace Go calls such as:

```go
conversation, err := store.OpenConversation(session.ID)
```

Keep tests that query `transcript_messages` unchanged, because they verify the persistent compatibility boundary. In schema comments, say `conversation message tables (legacy names: transcript_messages and transcript_tool_calls)` where a user-facing explanation is needed.

- [ ] **Step 4: Format and run focused project tests**

Run:

```bash
gofmt -w internal/project
go test ./internal/project -count=1
```

Expected: PASS. The database continues to use the original `transcript_*` table names.

- [ ] **Step 5: Commit**

```bash
git add internal/project
git commit -m "refactor: rename transcript store to conversation"
```

### Task 2: Rename runtime ownership and tool-loop call sites

**Files:**
- Modify: `internal/bootstrap/project_runtime.go`
- Modify: `internal/bootstrap/coordinator.go`
- Modify: `internal/bootstrap/skill_catalog.go`
- Modify: `internal/bootstrap/turn_service_facade.go`
- Modify: `internal/bootstrap/tools.go`
- Modify: `internal/bootstrap/*_test.go`
- Modify: `internal/project/turn/runtime.go`
- Modify: `internal/project/turn/service.go`
- Modify: `internal/project/turn/events.go`
- Modify: `internal/project/session/events.go`

**Interfaces:**
- Consumes: Task 1 `*project.ConversationStore` and `OpenConversation`.
- Produces: `ProjectRuntime.Conversation(sessionID string) *project.ConversationStore`; `turn.Runtime.Conversation(string) *project.ConversationStore`.

- [ ] **Step 1: Update runtime interfaces and session ownership**

Replace the session field and accessors with:

```go
type sessionRuntime struct {
    worker       *sessionstate.Worker
    conversation *project.ConversationStore
    surface      *project.ContextSurfaceStore
}

// Conversation 返回为下一次模型运行提供有序消息的存储对象。
func (runtime *ProjectRuntime) Conversation(sessionID string) *project.ConversationStore
```

Open it through `runtime.store.OpenConversation(session.ID)`, close `session.conversation`, and use `conversation` in lifecycle comments. In `internal/project/turn/runtime.go`, replace `Transcript(string) *project.TranscriptStore` with `Conversation(string) *project.ConversationStore`.

- [ ] **Step 2: Update the tool loop and skill catalog**

In `RunTurn`, use the exact local variable and failure wording:

```go
conversation := runtime.Conversation(session.ID)
if conversation == nil {
    return fmt.Errorf("session conversation is unavailable")
}
```

Replace every subsequent `transcript.Append` / `AppendBatch` call with `conversation.Append` / `AppendBatch`. Rename `ensureSessionSkillCatalog` parameter to `conversation *project.ConversationStore`, update its nil error to `session conversation is unavailable`, and update Coordinator callers to `runtime.Conversation(...)`.

- [ ] **Step 3: Update bootstrap tests and comments**

Rename test-local variables and test names that refer to the runtime message history, including:

```go
func TestCoordinatorMessagesReturnsConversation(t *testing.T)
```

Update only the semantic text/assertion messages to `conversation`; preserve SQL identifiers and fixture database contents that deliberately use `transcript_*` table names.

- [ ] **Step 4: Run focused bootstrap and turn tests**

Run:

```bash
gofmt -w internal/bootstrap internal/project/turn internal/project/session
go test ./internal/bootstrap ./internal/project/turn ./internal/project/session -count=1
```

Expected: PASS, including tests for turn persistence, tool-result ordering, restored sessions, and skill catalog injection.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap internal/project/turn internal/project/session
git commit -m "refactor: name session history conversation"
```

### Task 3: Rename context-layer source terminology

**Files:**
- Modify: `internal/project/context/assembler.go`
- Modify: `internal/project/context/assembler_test.go`
- Modify: `internal/project/context/compactor.go`
- Modify: `internal/project/context/compactor_test.go`
- Modify: `internal/project/context/config.go`
- Modify: `internal/project/context_surface.go`
- Modify: `internal/project/context_surface_test.go`
- Modify: `internal/project/context_meter.go`
- Modify: `internal/project/context_meter_test.go`

**Interfaces:**
- Consumes: Task 1 `loadConversationQueryer` and Task 2 `Runtime.Conversation`.
- Produces: context APIs and comments that describe the immutable source as a raw conversation, without renaming persisted SQL tables.

- [ ] **Step 1: Rename context-source methods and helper names**

Where interfaces currently expose `Transcript`, use `Conversation` with the same signatures except the identifier:

```go
type SessionSource interface {
    Conversation(string) ([]core.Message, error)
    ContextSurface(string) *ContextSurfaceStore
}
```

Rename variables such as `transcriptMessages` to `conversationMessages`, `surfaceTranscript` fixture helpers to `surfaceConversation`, and all private helper symbols containing `Transcript` to their `Conversation` equivalent. Use the new Task 1 loader helper wherever Context Surface reconstructs raw messages.

- [ ] **Step 2: Preserve the raw-conversation versus Context Surface contract**

Update comments and assertions to say:

```text
Context Surface never modifies the immutable raw conversation.
```

Do not change pruning semantics, source sequence numbers, table names, or snapshots. Continue testing that a pruned Surface does not alter the persisted messages returned by `OpenConversation`.

- [ ] **Step 3: Run focused context tests**

Run:

```bash
gofmt -w internal/project/context internal/project/context_surface.go internal/project/context_surface_test.go internal/project/context_meter.go internal/project/context_meter_test.go
go test ./internal/project/context ./internal/project -count=1
```

Expected: PASS, including disabled full-conversation replay, surface replacement, compaction, and overflow-recovery tests.

- [ ] **Step 4: Commit**

```bash
git add internal/project/context internal/project/context_surface.go internal/project/context_surface_test.go internal/project/context_meter.go internal/project/context_meter_test.go
git commit -m "refactor: describe context source as conversation"
```

### Task 4: Rename model/UI terminology and current documentation

**Files:**
- Modify: `internal/model/stepper.go`
- Modify: `internal/model/prompt.go`
- Modify: `internal/terminal/model.go`
- Modify: `internal/terminal/model_test.go`
- Modify: `README.md`
- Modify: `docs/TECHNICAL.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Consumes: completed Go-level `Conversation` terminology.
- Produces: user-facing documentation and UI comments/tests that call persisted session messages a conversation, while explicitly retaining database table identifiers as legacy schema names.

- [ ] **Step 1: Update model and terminal wording**

Rename UI-private symbols only where they name the persisted history rather than a rendering implementation detail. For example, rename `renderTranscriptBlock` to `renderConversationBlock`, update test names such as `TestRenderMessageSeparatesConversationRoles`, and replace comments stating that data does not enter the transcript with `does not enter the persisted conversation`.

Update model comments to state that the model adapter has no persisted conversation state and that reasoning preservation maintains conversation persistence fidelity.

- [ ] **Step 2: Update current product documentation**

In `README.md`, `docs/TECHNICAL.md`, and `docs/ARCHITECTURE.md`, replace runtime terminology with `conversation` / `对话记录` / `持久化会话消息历史` as appropriate. In database schema sections, retain the exact SQL identifiers in code blocks:

```text
transcript_messages
transcript_tool_calls
```

and explain once that these are legacy SQLite table names retained for existing project database compatibility.

- [ ] **Step 3: Do not edit archived plans**

Leave all earlier dated files under `docs/superpowers/plans/` unchanged, except this newly created plan. They document historical implementation decisions and are not active product terminology.

- [ ] **Step 4: Run presentation and documentation checks**

Run:

```bash
gofmt -w internal/model internal/terminal
go test ./internal/model ./internal/terminal -count=1
git diff --check
```

Expected: PASS and no whitespace errors.

- [ ] **Step 5: Commit**

```bash
git add internal/model internal/terminal README.md docs/TECHNICAL.md docs/ARCHITECTURE.md
git commit -m "docs: call persisted history conversation"
```

### Task 5: Verify the full rename and compatibility boundary

**Files:**
- Modify only if verification reveals an unrenamed active runtime-facing identifier.

**Interfaces:**
- Consumes: all prior rename tasks.
- Produces: a passing full suite and an intentional remaining `transcript_*` schema-only reference set.

- [ ] **Step 1: Audit active source references**

Run:

```bash
rg -n -i 'transcript' --glob '*.go' --glob 'README.md' --glob 'docs/TECHNICAL.md' --glob 'docs/ARCHITECTURE.md' .
```

Expected: remaining runtime-source occurrences are only exact SQLite identifiers, migration compatibility comments, or explicit documentation noting legacy table names. Rename any remaining Go API/type/field/method/test/comment use that still names the semantic runtime history `transcript`.

- [ ] **Step 2: Run the full validation suite**

Run:

```bash
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
```

Expected: all commands exit successfully.

- [ ] **Step 3: Review database compatibility**

Confirm no migration changes, table renames, data copies, or schema-version changes were introduced. A pre-existing `.pentgo/pentgo.db` must continue to open because the SQL table names remain `transcript_messages` and `transcript_tool_calls`.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "test: verify conversation terminology migration"
```

## Self-Review

- **Spec coverage:** Tasks 1–3 rename the storage, runtime, and context APIs. Task 4 covers model/UI wording and current user documentation. Task 5 verifies full behavior and explicitly protects existing SQLite schemas. Historical plans remain excluded as required.
- **Placeholder scan:** Every task identifies files, interfaces, concrete symbol names, code snippets where APIs change, and exact verification commands. No deferred implementation placeholders remain.
- **Type consistency:** Task 1 defines `ConversationStore` and `OpenConversation`; Task 2 exposes `ProjectRuntime.Conversation` and `turn.Runtime.Conversation`; Task 3 consumes those exact names. No legacy Go API is retained.
