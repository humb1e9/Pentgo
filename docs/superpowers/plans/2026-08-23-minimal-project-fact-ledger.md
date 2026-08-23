# Minimal Project Fact Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the over-modeled Phase 2 project-fact graph with a minimal, typed, auditable key/value ledger whose Fact Index is captured once at turn start and reused for the whole turn.

**Architecture:** `domain.ProjectFact` contains only `Key`, `Value`, optional `EvidenceRef`, and `UpdatedAt`. A small application `ProjectFactLedger` owns validation and Evidence-existence rules, while a SQLite `ProjectFactRepository` only persists the minimum row. A separate read-only Fact Index renderer consumes a ledger list and produces one fixed 4,096-rune snapshot; `TurnService` captures that snapshot once immediately after beginning a turn and passes the same text to every normal and overflow-recovery model request. Host tools are protocol adapters over typed ledger commands, not business-rule owners.

**Tech Stack:** Go, SQLite via `modernc.org/sqlite`, existing `database/sql` stores, Eino streamed model loop, Bubble Tea CLI, Go `testing`, race detector, `go vet`, and cross-platform `go build`.

## Global Constraints

- Preserve immutable `transcript_messages`, `transcript_tool_calls`, and `evidence_records` audit behavior.
- Do not migrate or preserve any existing v5/v6 project-fact data; drop old fact tables and start the minimal ledger empty.
- Fact key must match `^[a-z][a-z0-9_]{0,63}$`.
- Fact value must be non-empty and at most `16,384 Unicode runes`.
- `EvidenceRef` is optional and only requires an existing Evidence row in the current project; success status is irrelevant.
- Every upsert fully replaces the key's value and evidence reference; an omitted reference clears the previous reference.
- Expose exactly `upsert_project_fact`, `get_project_fact`, and `list_project_facts`; do not retain search, delete, deprecate, restore, category, confidence, pinned, edges, Blackboard, or compatibility aliases.
- Fact Index is generated once per turn, injected into every request in that turn, and refreshed only on the next turn.
- Fact Index and list output are sorted by key ascending and bounded to a fixed `4,096 Unicode runes`, with shown/omitted metadata.
- Fact Index and list output show `[evidence_ref: N]` when present; full values are returned only by `get_project_fact`.
- Fact content is untrusted model-visible data: flatten line breaks/tabs and HTML-escape key/value before placing it inside the XML-ish envelope.
- Do not retain `fact_index_ratio`, `blackboard_ratio`, Fact Index-specific activity, or Fact Index-specific token measurement fields.
- Tool failures continue through the existing host Evidence executor and are recorded as failed Evidence; transcript ordering and tool-call/result pairing remain unchanged.
- Do not modify unrelated working-tree changes.

---

### Task 1: Replace the domain contract with the minimal fact type

**Files:**
- Modify: `internal/domain/project_facts.go`
- Modify: `internal/domain/model.go`
- Modify: `internal/domain/model_test.go`
- Create: `internal/app/project_fact_ledger.go`
- Test: `internal/app/project_fact_ledger_test.go`

**Interfaces:**
- Produces `domain.ProjectFact{Key string, Value string, EvidenceRef *int, UpdatedAt time.Time}`.
- Produces `domain.ValidateProjectFact(domain.ProjectFact) error` and key/value constants:
  `MaxProjectFactKeyRunes = 64`, `MaxProjectFactValueRunes = 16*1024`.
- Produces application types:
  ```go
  type ProjectFactUpsert struct {
      Key         string
      Value       string
      EvidenceRef *int
  }

  type ProjectFactRepository interface {
      Upsert(context.Context, domain.ProjectFact) error
      Get(context.Context, string) (domain.ProjectFact, bool, error)
      List(context.Context) ([]domain.ProjectFact, error)
  }

  type EvidenceReferenceLookup interface {
      Exists(int) bool
  }

  type ProjectFactLedger struct { ... }
  func NewProjectFactLedger(ProjectFactRepository, EvidenceReferenceLookup) *ProjectFactLedger
  func (*ProjectFactLedger) Upsert(context.Context, ProjectFactUpsert) error
  func (*ProjectFactLedger) Get(context.Context, string) (domain.ProjectFact, bool, error)
  func (*ProjectFactLedger) List(context.Context) ([]domain.ProjectFact, error)
  ```

- [ ] **Step 1: Write failing domain validation tests**

  Add table-driven cases for valid keys such as `api_base_url`, `a1`, and `login_endpoint_v2`, and invalid keys such as empty, uppercase, leading digit, hyphenated, whitespace-containing, and 65-rune keys. Add value tests for empty, exactly `16,384` runes, and `16,385` runes, including multibyte Unicode.

- [ ] **Step 2: Run the domain tests and verify they fail against the old contract**

  Run:
  ```bash
  go test ./internal/domain -run 'ProjectFact|Fact' -count=1
  ```
  Expected: FAIL because the current domain contract still contains the over-modeled fields or lacks the new minimal validation.

- [ ] **Step 3: Implement the minimal domain type and ledger service**

  Remove category, confidence, pinned, edge, Evidence-ref slice, ID, and project/session fields from the domain fact contract. Implement `ValidateProjectFact` with the exact key regex and rune limits. Implement `ProjectFactLedger.Upsert` so it validates the typed command, checks `EvidenceRef == nil` or `evidence.Exists(ref)`, and passes a value copy to the repository. The repository sets `UpdatedAt` when it persists the row. Do not inspect Evidence success status.

- [ ] **Step 4: Add service-rule tests**

  Use an in-memory fake repository and fake Evidence lookup to prove: nil Evidence is accepted; existing successful and failed refs are both accepted; missing refs are rejected; invalid key/value is rejected before repository mutation; and every upsert sends the exact optional pointer. Cover repository-owned `UpdatedAt` with SQLite persistence tests.

- [ ] **Step 5: Run the task tests**

  Run:
  ```bash
  go test ./internal/domain ./internal/app -run 'ProjectFact|FactLedger' -count=1
  ```
  Expected: PASS.

- [ ] **Step 6: Commit the domain/service boundary**

  ```bash
  git add internal/domain/project_facts.go internal/domain/model.go internal/domain/model_test.go internal/app/project_fact_ledger.go internal/app/project_fact_ledger_test.go
  git commit -m "refactor: simplify project fact domain"
  ```

### Task 2: Replace SQLite fact storage and discard old fact data

**Files:**
- Modify: `internal/adapters/storage/sqlite.go`
- Modify: `internal/adapters/storage/project_fact_store.go` (rename the concrete type in-place to `ProjectFactRepository` or split into `project_fact_repository.go` if the file becomes clearer)
- Modify: `internal/adapters/storage/project_store.go`
- Modify: `internal/adapters/storage/evidence_store.go`
- Modify: `internal/adapters/storage/project_fact_store_test.go` (replace with minimal repository tests)
- Modify: `internal/adapters/storage/project_store_test.go`
- Test: `internal/adapters/storage/sqlite_test.go` if migration coverage is not already located in `project_fact_store_test.go`

**Interfaces:**
- Produces `ProjectStore.OpenProjectFactRepository() (*ProjectFactRepository, error)`.
- `ProjectFactRepository` implements the application `ProjectFactRepository` interface with:
  ```go
  Upsert(context.Context, domain.ProjectFact) error
  Get(context.Context, string) (domain.ProjectFact, bool, error)
  List(context.Context) ([]domain.ProjectFact, error)
  ```
- Adds `EvidenceStore.Exists(sequence int) bool`, implemented as a locked existence lookup in `evidence_records`.

- [ ] **Step 1: Write migration and repository failing tests**

  Add tests that create a current fixture with `projects`, `transcript_messages`, `evidence_records`, legacy `facts`, `project_facts`, `project_fact_evidence`, and `project_fact_edges`, set `user_version` to 6, open it through `OpenProjectStore`, and assert after migration that all old fact tables/rows are gone, the new `project_facts` table exists empty, and transcript/Evidence rows remain. Add repository tests for insert, replacement, nil evidence, nullable evidence, key sorting, missing get, and cancellation.

- [ ] **Step 2: Run storage tests to verify the old schema/API fails the new expectations**

  Run:
  ```bash
  go test ./internal/adapters/storage -run 'ProjectFact|SQLite|Migration' -count=1
  ```
  Expected: FAIL because schema version 6 and the graph-based tables/API still exist.

- [ ] **Step 3: Implement schema version 7 and the destructive fact reset**

  Raise `schemaVersion` to 7. Remove all graph-based fact tables from the current schema definition and create only:
  ```sql
  CREATE TABLE IF NOT EXISTS project_facts (
      project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      fact_key TEXT NOT NULL,
      value TEXT NOT NULL,
      evidence_ref INTEGER NULL REFERENCES evidence_records(seq),
      updated_at INTEGER NOT NULL,
      PRIMARY KEY (project_id, fact_key)
  );
  ```
  In the v6-to-v7 migration transaction, drop `facts`, `project_facts`, `project_fact_evidence`, and `project_fact_edges` if present, then create the new table and update the schema version. Do not copy any rows. Keep transcript, tool-call, Evidence, session, and Context Surface tables untouched.

- [ ] **Step 4: Implement the repository with one-row replacement**

  Replace graph queries and evidence-join loading with parameterized SQL scoped by the repository’s singleton `projectID`. `Upsert` must use `INSERT ... ON CONFLICT(project_id, fact_key) DO UPDATE SET value = excluded.value, evidence_ref = excluded.evidence_ref, updated_at = excluded.updated_at`. `Get` returns `(zero, false, nil)` for a missing key. `List` selects only the current project and orders `fact_key ASC`. Close the repository by closing no independent connection; `ProjectStore.Close` remains the sole owner of the shared database.

- [ ] **Step 5: Add Evidence existence support**

  Implement `EvidenceStore.Exists` without exposing Evidence content or success semantics to the fact ledger. Add tests for existing success, existing failure, missing, and non-positive refs.

- [ ] **Step 6: Run storage tests and commit**

  Run:
  ```bash
  go test ./internal/adapters/storage -run 'ProjectFact|SQLite|Migration|Evidence' -count=1
  ```
  Expected: PASS.

  ```bash
  git add internal/adapters/storage/sqlite.go internal/adapters/storage/project_fact_store.go internal/adapters/storage/project_store.go internal/adapters/storage/evidence_store.go internal/adapters/storage/project_fact_store_test.go internal/adapters/storage/project_store_test.go
  git commit -m "refactor: reset storage to minimal project facts"
  ```

### Task 3: Add a pure, fixed-size Fact Index projection

**Files:**
- Create: `internal/app/project_fact_index.go`
- Create: `internal/app/project_fact_index_test.go`
- Modify: `internal/app/project_fact_ledger.go`

**Interfaces:**
- Produces:
  ```go
  const ProjectFactIndexMaxRunes = 4096
  type ProjectFactIndex struct { ... }
  func NewProjectFactIndex(interface{ List(context.Context) ([]domain.ProjectFact, error) }) *ProjectFactIndex
  func (*ProjectFactIndex) Snapshot(context.Context) (string, error)
  func RenderProjectFactIndex([]domain.ProjectFact) string
  ```

- [ ] **Step 1: Write projection tests first**

  Test key sorting independent of insertion order; evidence suffix rendering; value newline/tab flattening; HTML escaping of `<`, `>`, `&`, and quotes; exact `shown`/`omitted` counts; empty index; and a corpus whose rendered result is at most `4,096` runes including envelope/footer.

- [ ] **Step 2: Run projection tests and verify the renderer is absent**

  ```bash
  go test ./internal/app -run 'ProjectFactIndex' -count=1
  ```
  Expected: FAIL because the new renderer does not exist.

- [ ] **Step 3: Implement the pure renderer**

  Copy the input slice, sort by `Key ASC`, calculate the fixed envelope/footer cost, append complete lines only while the full output remains within 4,096 runes, then compute omitted as `len(all)-shown`. Use the exact line shape:
  ```text
  - key: value [evidence_ref: N]
  ```
  Always include the stable guidance footer with `get_project_fact` and `list_project_facts`. Escape and flatten only at rendering time; do not mutate persisted values.

- [ ] **Step 4: Run projection tests and commit**

  ```bash
  go test ./internal/app -run 'ProjectFactIndex' -count=1
  git add internal/app/project_fact_index.go internal/app/project_fact_index_test.go internal/app/project_fact_ledger.go
  git commit -m "feat: add fixed project fact index"
  ```

### Task 4: Make the runtime own one Fact Index snapshot per turn

**Files:**
- Modify: `internal/app/project_runtime.go`
- Modify: `internal/app/turn_service.go`
- Modify: `internal/app/context_assembler.go`
- Modify: `internal/app/context_compactor.go`
- Modify: `internal/app/context_assembler_test.go`
- Modify: `internal/app/turn_service_test.go`
- Modify: `internal/app/context_meter.go`
- Modify: `internal/app/context_meter_test.go`
- Modify: `internal/agent/turn.go` only if the request contract needs a comment/name correction

**Interfaces:**
- Change `ContextPreparer` to:
  ```go
  Prepare(context.Context, string, string, []agent.Tool, string) (agent.ModelStepInput, []agent.ContextActivity, error)
  PrepareOverflowRecovery(context.Context, string, string, []agent.Tool, string) (agent.ModelStepInput, []agent.ContextActivity, error)
  ```
  The final string is the already-rendered turn snapshot.
- `ProjectRuntime` exposes `ProjectFacts() *ProjectFactLedger` and `ProjectFactIndex() *ProjectFactIndex`; it no longer opens/closes the old fact store directly.

- [ ] **Step 1: Add failing turn-snapshot tests**

  Add a fake `ContextPreparer` that records every supplied Fact Index string. Run a turn whose first model response invokes `upsert_project_fact`, force a second model request, and assert both requests received the same pre-tool snapshot. Run a second turn and assert it receives the newly written fact. Add an overflow-recovery case and assert recovery receives the same string as the original request.

- [ ] **Step 2: Add disabled-context injection regression**

  Update the disabled-context assembler test so a non-empty supplied snapshot appears in `ModelStepInput.ProjectFacts` even when `ContextWindow == 0`; no repository read should occur in the assembler.

- [ ] **Step 3: Implement runtime ownership and turn capture**

  During runtime construction, open the repository, create `ProjectFactLedger` with the Evidence lookup, create `ProjectFactIndex`, and retain both as runtime dependencies. Do not add an independent close path for the repository. In `TurnService.RunTurn`, immediately after `BeginTurn` and before any tool execution, call `runtime.ProjectFactIndex().Snapshot(ctx)` once. Pass that returned text to every `Prepare` and `PrepareOverflowRecovery` call in the turn. Remove `ContextAssembler.factIndex()` and all per-request database reads. `ContextAssembler.materialize` must always pass the supplied snapshot through, whether context compaction is enabled or disabled.

- [ ] **Step 4: Remove obsolete measurement/activity plumbing**

  Remove `FactIndexRatio` budgeting, `ContextFactIndexLimited`, and `FactIndexTokens` from policy/activity/measurement paths. Keep `ContextRequest.FactIndex` only as the exact final envelope input used by `ContextMeter` to count the provider-visible request. Ensure compactor requests reuse the supplied envelope and never fetch facts.

- [ ] **Step 5: Run context and turn tests**

  ```bash
  go test ./internal/app -run 'Context|Turn|FactIndex' -count=1
  ```
  Expected: PASS, including proof that same-turn writes appear only on the next turn.

- [ ] **Step 6: Commit the turn snapshot boundary**

  ```bash
  git add internal/app/project_runtime.go internal/app/turn_service.go internal/app/context_assembler.go internal/app/context_compactor.go internal/app/context_assembler_test.go internal/app/turn_service_test.go internal/app/context_meter.go internal/app/context_meter_test.go internal/agent/turn.go
  git commit -m "refactor: snapshot project facts once per turn"
  ```

### Task 5: Replace the six graph tools with three typed CRUD adapters

**Files:**
- Modify: `internal/app/tools.go`
- Create or modify: `internal/app/tools_test.go`
- Modify: `internal/app/tool_provider.go`
- Modify: `internal/app/tool_provider_test.go`
- Modify: `internal/app/turn_service_test.go`

**Interfaces:**
- `runtimeToolProvider.Tools` exposes exactly these project-fact names:
  `upsert_project_fact`, `get_project_fact`, `list_project_facts`.
- Tool schemas:
  - upsert required `key`, `value`; optional `evidence_ref` integer/null;
  - get required `key`;
  - list has no required fields and no filters.

- [ ] **Step 1: Rewrite tool tests to the minimal contract**

  Replace lifecycle/edge/search/deprecate/restore tests with tests for exact three-name exposure, absence of all removed names, schema required fields, key/value boundaries, optional evidence decoding, and typed ledger invocation. Test `get` returns complete value and evidence suffix, missing legal key returns `project fact not found` with nil error, and `list` uses the fixed index format/order.

- [ ] **Step 2: Run the rewritten tests against the old tools**

  ```bash
  go test ./internal/app -run 'StructuredProjectFact|ProjectFactTool' -count=1
  ```
  Expected: FAIL because the old six-tool dispatcher and graph schemas are still exposed.

- [ ] **Step 3: Implement typed adapters**

  Replace `projectFactToolKind` dispatch and generic graph argument decoders with three narrow structs. Decode JSON into typed values at the boundary; validate integer/null `evidence_ref`, reject unknown malformed values, and call `ProjectFactLedger`. Do not duplicate key/value/Evidence business rules in the tool. Use `RenderProjectFactIndex` for list output. Preserve host Evidence behavior for all returned errors.

- [ ] **Step 4: Update collision protection**

  Change `localToolReservedNames`, provider collision tests, and turn-service tool-list expectations from six names to the exact three names. Ensure `search_project_facts`, `deprecate_project_fact`, `restore_project_fact`, and `write_project_fact` cannot appear through built-in registration or compatibility aliases.

- [ ] **Step 5: Run tool tests and commit**

  ```bash
  go test ./internal/app -run 'ProjectFact|ToolProvider|TurnService' -count=1
  git add internal/app/tools.go internal/app/tools_test.go internal/app/tool_provider.go internal/app/tool_provider_test.go internal/app/turn_service_test.go
  git commit -m "refactor: expose minimal project fact tools"
  ```

### Task 6: Remove obsolete configuration and update CLI, prompt, and documentation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/adapters/llm/prompt.go`
- Modify: `internal/cli/model.go`
- Modify: `internal/cli/model_test.go`
- Modify: `internal/app/coordinator.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/TECHNICAL.md`

**Interfaces:**
- `AgentContextConfig` no longer has `FactIndexRatio`; `Effective` and validation retain only Phase 1 context controls.
- `Coordinator.FactIndex()` returns the current fixed Fact Index snapshot for `/facts` display.

- [ ] **Step 1: Update failing configuration and CLI tests**

  Remove tests expecting `fact_index_ratio` defaults/rejection and replace them with assertions that the context policy contains no fact-index ratio. Add `/facts` output tests that verify key sorting and the fixed envelope. Add prompt/tool-list tests asserting only the three minimal tools are described.

- [ ] **Step 2: Run configuration and CLI tests to identify old references**

  ```bash
  go test ./internal/config ./internal/cli ./internal/adapters/llm -run 'Fact|Blackboard|Prompt|Command' -count=1
  ```
  Expected: FAIL until old ratio/activity/prompt references are removed.

- [ ] **Step 3: Remove obsolete configuration and activity code**

  Delete `FactIndexRatio`, its default/validation, and any `blackboard_ratio` migration rejection branch. Remove `ContextFactIndexLimited` terminal status handling and related tests. Keep `/facts`, but route it through the runtime’s fixed index provider rather than old storage formatting.

- [ ] **Step 4: Rewrite prompt and public docs**

  Replace all structured graph terminology with the minimal key/value ledger contract. Document the three tools, optional Evidence reference semantics, full replacement behavior, key regex, value rune limit, fixed 4,096-rune turn snapshot, and same-turn visibility rule. Remove all user-facing Blackboard, ratio, category, confidence, edge, search, delete, deprecate, and restore language. Historical superpowers planning documents remain archival and are not edited as product documentation.

- [ ] **Step 5: Run CLI/config/prompt tests and commit**

  ```bash
  go test ./internal/config ./internal/cli ./internal/adapters/llm -count=1
  git add internal/config/config.go internal/config/config_test.go internal/adapters/llm/prompt.go internal/cli/model.go internal/cli/model_test.go internal/app/coordinator.go README.md docs/ARCHITECTURE.md docs/TECHNICAL.md
  git commit -m "docs: describe minimal project fact ledger"
  ```

### Task 7: Remove stale Phase 2 implementation and add end-to-end regression coverage

**Files:**
- Modify: `internal/domain/project_facts.go`
- Modify: `internal/domain/model.go`
- Modify: `internal/adapters/storage/sqlite.go`
- Modify: `internal/adapters/storage/project_fact_store.go`
- Modify: `internal/adapters/storage/project_store.go`
- Modify: `internal/adapters/storage/evidence_store.go`
- Modify: `internal/app/project_fact_ledger.go`
- Modify: `internal/app/project_fact_index.go`
- Modify: `internal/app/project_runtime.go`
- Modify: `internal/app/turn_service.go`
- Modify: `internal/app/context_assembler.go`
- Modify: `internal/app/context_compactor.go`
- Modify: `internal/app/context_meter.go`
- Modify: `internal/app/tools.go`
- Modify: `internal/app/tool_provider.go`
- Modify: `internal/app/coordinator.go`
- Modify: `internal/config/config.go`
- Modify: `internal/adapters/llm/prompt.go`
- Modify: `internal/cli/model.go`
- Test: `internal/app/turn_service_test.go`
- Test: `internal/app/context_assembler_test.go`
- Test: `internal/app/context_meter_test.go`
- Test: `internal/app/tools_test.go`
- Test: `internal/app/tool_provider_test.go`
- Test: `internal/adapters/storage/project_store_test.go`
- Test: `internal/adapters/storage/project_fact_store_test.go`
- Test: `internal/adapters/storage/evidence_store_test.go`
- Test: `internal/domain/model_test.go`
- Delete: `internal/adapters/storage/project_fact_store_test.go` graph-specific cases after replacing them with the minimal repository cases in the same file; retain unrelated storage coverage

**Interfaces:**
- No new public API. This task proves the replacement is complete rather than adding another compatibility layer.

- [ ] **Step 1: Add end-to-end lifecycle tests**

  Cover: upsert with no ref; upsert with existing success ref; upsert with existing failed ref; missing/invalid ref rejection; full replacement clears an old ref; get found/not-found; list key order; value/body rune boundary; fixed index truncation and escaped untrusted content; same-turn snapshot isolation; next-turn visibility; disabled and enabled context both receiving the supplied snapshot; and transcript/Evidence rows remaining intact across the schema reset.

- [ ] **Step 2: Search for forbidden runtime symbols**

  Run:
  ```bash
  grep -RInE 'Blackboard|write_project_fact|blackboard_ratio|fact_index_ratio|FactIndexTokens|ContextFactIndexLimited|project_fact_edges|project_fact_evidence|deprecate_project_fact|restore_project_fact|search_project_facts' --include='*.go' --include='*.md' .
  ```
  Expected: no matches in production Go, README, architecture, technical docs, or active tests. Archival superpowers plans may retain historical references and must be excluded from the production grep if they are intentionally preserved.

- [ ] **Step 3: Run the complete non-race suite**

  ```bash
  TMPDIR="$HOME/.cache/pentgo-go-tmp" GOTMPDIR="$HOME/.cache/pentgo-go-tmp" go test ./... -count=1
  ```
  Expected: PASS for every package.

- [ ] **Step 4: Review the final diff for root-cause separation**

  Confirm manually that: tools contain no business validation beyond decoding; ContextAssembler contains no fact-store reads; TurnService owns exactly one turn-start snapshot; repository contains no graph/category/confidence logic; evidence lookup only checks existence; and no old migration code attempts to preserve fact data.

- [ ] **Step 5: Commit the regression and cleanup work**

  ```bash
  git add -A
  git commit -m "test: verify minimal project fact replacement"
  ```

### Task 8: Run the final quality matrix and verify the repository state

**Files:**
- No source changes expected; fix only issues exposed by the commands, with a focused test and commit if a fix is required.

- [ ] **Step 1: Format and whitespace check**

  ```bash
  gofmt -w $(git diff --name-only -- '*.go')
  git diff --check
  ```
  Expected: no whitespace errors and no unformatted Go files.

- [ ] **Step 2: Run race tests**

  ```bash
  TMPDIR="$HOME/.cache/pentgo-go-tmp" GOTMPDIR="$HOME/.cache/pentgo-go-tmp" go test ./... -race -count=1
  ```
  Expected: PASS with no race reports.

- [ ] **Step 3: Run vet and builds**

  ```bash
  go vet ./...
  go build ./...
  GOOS=windows GOARCH=amd64 go build ./...
  GOOS=darwin GOARCH=arm64 go build ./...
  ```
  Expected: all commands exit 0.

- [ ] **Step 4: Verify only intended commits and a clean tree**

  ```bash
  git status --short
  git log --oneline -6
  ```
  Expected: no untracked or unstaged files; the minimal-ledger commits are visible above the existing Phase 1 history.

---

## Self-review against the approved specification

- Minimal model and exact key/value rules: Tasks 1–2.
- Existing Evidence lookup without success gating: Tasks 1–2 and Task 7.
- Full replacement and ref clearing: Tasks 1–2 and Task 7.
- Single-table destructive schema reset with transcript/Evidence preservation: Task 2 and Task 7.
- Separate ledger, repository, and read-only projection boundaries: Tasks 1–3.
- One Fact Index snapshot per turn, reused across tool loops and overflow recovery: Task 4 and Task 7.
- Fixed 4,096-rune key-sorted index with safe rendering: Task 3 and Task 7.
- Exactly three tools and no compatibility aliases: Task 5 and Task 7.
- Configuration, CLI, prompt, and documentation cutover: Task 6.
- Full verification and clean commit state: Tasks 7–8.
