# Context Surface Phase 2 Project Facts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Phase 1 Blackboard with a durable structured project-fact ledger, deterministic bounded Fact Index, graph relations, Evidence-constrained host tools, and an atomic migration of existing project facts.

**Architecture:** Project facts become normalized SQLite records owned by a `storage.ProjectFactStore`; `ProjectRuntime` exposes that store to the host-only fact tools and the `ContextAssembler` renders its deterministic Fact Index before every enabled provider request. The raw transcript and Evidence journal remain immutable. Fact writes, Evidence references, and graph edges are validated in the storage transaction; the model receives only a bounded index and must use host tools for complete details.

**Tech Stack:** Go 1.25, SQLite (`modernc.org/sqlite`), Eino tool schemas, existing host-owned `agent.Tool` loop.

## Global Constraints

- Preserve raw transcript messages and Evidence rows; Phase 2 may only replace the Phase 1 project facts table and prompt projection.
- Do not preserve `write_project_fact`; expose only `upsert_project_fact`, `get_project_fact`, `list_project_facts`, `search_project_facts`, `deprecate_project_fact`, and `restore_project_fact`.
- Replace `agent.context.blackboard_ratio` completely with `agent.context.fact_index_ratio`; old configuration must fail validation rather than silently fall back.
- `upsert_project_fact` writes its fact and supplied edges atomically.
- A `confirmed` fact requires one or more successful Evidence rows in the current project database; `tentative` facts may be evidence-free; `deprecated` facts and edges remain auditable but are excluded from the default index.
- The Fact Index is deterministic: pinned first; then `target`, `finding`/`chain`, `exploit`/`poc`, `auth`/`infra`/`business`, and `note`; ties use latest update descending then fact key ascending.
- The Fact Index includes key, category, summary, confidence, and compact non-deprecated graph hints; it never contains full Body.
- All discovery and tool results are bounded and validate untrusted tool arguments at the host boundary.
- Run the complete normal, race, vet, native, Windows, and Darwin verification commands before final commit.

---

### Task 1: Define structured fact contracts and replace context configuration

**Files:**
- Modify: `internal/domain/model.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/agent/context.go`
- Modify: `internal/app/context_meter.go`
- Modify: `internal/app/context_meter_test.go`

**Interfaces:**
- Produces `domain.ProjectFact`, `domain.ProjectFactEdge`, enum validation helpers, and bounded `domain.FactIndex` rendering input.
- Replaces `AgentContextConfig.BlackboardRatio` with `FactIndexRatio` (`json:"fact_index_ratio,omitempty"`).
- Replaces `ContextMeasurement.BlackboardTokens` and `ContextRequest.Blackboard` with `FactIndexTokens` and `FactIndex`.

- [ ] **Step 1: Write failing domain/config tests**

Add table-driven tests that accept every required category and confidence, reject empty key/summary/body and invalid enum values, and prove `confirmed` without Evidence refs is rejected by the store-facing validation input. Add config tests that prove `fact_index_ratio` defaults to `0.08`, must be positive and lower than `threshold_ratio`, and that JSON containing `blackboard_ratio` is rejected as an unknown/unsupported migration field.

```go
func TestAgentContextFactIndexRatio(t *testing.T) {
    cfg := AgentContextConfig{ContextWindow: 1000}.Effective()
    if cfg.FactIndexRatio != 0.08 { t.Fatalf("ratio = %v", cfg.FactIndexRatio) }
    for _, cfg := range []AgentContextConfig{
        {ContextWindow: 1000, FactIndexRatio: -0.1},
        {ContextWindow: 1000, FactIndexRatio: 0.8, ThresholdRatio: 0.8},
    } {
        if err := cfg.Validate(); err == nil { t.Fatal("invalid fact index ratio accepted") }
    }
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/domain ./internal/config ./internal/app -run 'Fact|Context' -count=1`

Expected: FAIL because the structured types and `FactIndexRatio` do not exist.

- [ ] **Step 3: Add the contracts and validation**

Add the following exact domain model (with clone helpers for slices):

```go
type ProjectFact struct {
    ID string; ProjectID string; FactKey string; Category string
    Summary string; Body string; Confidence string; Pinned bool
    SourceSessionID string; EvidenceRefs []int
    CreatedAt time.Time; UpdatedAt time.Time
}
type ProjectFactEdge struct {
    ID string; ProjectID string; SourceFactKey string; TargetFactKey string
    EdgeType string; Confidence string; CreatedAt time.Time; UpdatedAt time.Time
}
```

Define category constants `target`, `auth`, `infra`, `business`, `finding`, `chain`, `exploit`, `poc`, `note`; edge constants `depends_on`, `leads_to`, `enables`, `exploits`, `discovered_on`, `contains`, `part_of`, `supports`; and confidence constants `confirmed`, `tentative`, `deprecated`. Validate trimmed strings, positive unique evidence refs, and only those enum values. Rename context configuration/measurement/request members to Fact Index terminology and update the context meter to count the exact final system message plus its bounded Fact Index.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/domain ./internal/config ./internal/app -run 'Fact|Context' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model.go internal/domain/model_test.go internal/config/config.go internal/config/config_test.go internal/agent/context.go internal/app/context_meter.go internal/app/context_meter_test.go
git commit -m "feat: define structured project fact contracts"
```

### Task 2: Add SQLite fact ledger, graph, and legacy Blackboard migration

**Files:**
- Modify: `internal/adapters/storage/sqlite.go`
- Create: `internal/adapters/storage/project_fact_store.go`
- Create: `internal/adapters/storage/project_fact_store_test.go`
- Modify: `internal/adapters/storage/project_store.go`

**Interfaces:**
- Produces `(*ProjectStore).OpenProjectFacts() (*ProjectFactStore, error)`.
- `ProjectFactStore` exposes `Upsert(context.Context, ProjectFactWrite)`, `Get(key)`, `List(FactQuery)`, `Search(query, FactQuery)`, `Deprecate(key)`, `Restore(key, confidence)`, and `FactIndex(tokenBudget)`.
- `ProjectFactWrite` carries one fact and its replacement/upsert edge set.

- [ ] **Step 1: Write storage migration and invariant tests**

Create v5 fixture database rows in legacy `facts`, open it through `OpenProjectStore`, and assert migration creates exactly one `note`/`tentative` fact per key preserving key, value as Body/Summary, session, and timestamps. Test confirmed Evidence enforcement, atomic rollback when an edge target does not exist, deprecated fact/edge audit retention, and deterministic Fact Index ordering/truncation.

```go
func TestV5FactsMigrateToTentativeNotes(t *testing.T) {
    store := openV5FactFixture(t, []legacyFact{{Key: "host", Value: "10.0.0.8"}})
    facts, err := store.OpenProjectFacts()
    if err != nil { t.Fatal(err) }
    got, found, err := facts.Get("host")
    if err != nil || !found || got.Category != domain.FactCategoryNote || got.Confidence != domain.FactConfidenceTentative {
        t.Fatalf("fact = %#v found=%v err=%v", got, found, err)
    }
}
```

- [ ] **Step 2: Run the focused storage tests and confirm failure**

Run: `go test ./internal/adapters/storage -run 'Fact|V5' -count=1`

Expected: FAIL because schema v6 and `ProjectFactStore` do not exist.

- [ ] **Step 3: Implement schema v6 and fact store**

Increase `schemaVersion` to 6. Remove the Phase 1 `facts` table from the new schema and add:

```sql
CREATE TABLE project_facts (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  fact_key TEXT NOT NULL, category TEXT NOT NULL, summary TEXT NOT NULL, body TEXT NOT NULL,
  confidence TEXT NOT NULL, pinned INTEGER NOT NULL CHECK(pinned IN (0,1)),
  source_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(project_id, fact_key)
);
CREATE TABLE project_fact_evidence (
  fact_id TEXT NOT NULL REFERENCES project_facts(id) ON DELETE CASCADE,
  evidence_seq INTEGER NOT NULL REFERENCES evidence_records(seq),
  PRIMARY KEY(fact_id, evidence_seq)
);
CREATE TABLE project_fact_edges (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL,
  source_fact_key TEXT NOT NULL, target_fact_key TEXT NOT NULL,
  edge_type TEXT NOT NULL, confidence TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  UNIQUE(project_id, source_fact_key, target_fact_key, edge_type),
  FOREIGN KEY(project_id, source_fact_key) REFERENCES project_facts(project_id, fact_key),
  FOREIGN KEY(project_id, target_fact_key) REFERENCES project_facts(project_id, fact_key)
);
```

Within one v5→v6 transaction, read the singleton project ID, copy every legacy fact to `project_facts` as `note` / `tentative` with `summary=value`, `body=value`, a deterministic `legacy:<key>` ID, preserved `session_id`, `at`, and `updated_at`; then drop `facts`. `Upsert` must verify every confirmed evidence ref is successful before it writes the fact/evidence join/edges; use a single transaction and roll back all fact updates if any validation or edge upsert fails. `Deprecate` only sets confidence to deprecated. `Restore` rejects restored `confirmed` facts without successful existing Evidence refs.

Implement `FactIndex` with bounded generated text, default exclusion of deprecated facts and edges, priority order, compact `source -[edge_type]-> target` hints, and shown/omitted/truncated metadata.

- [ ] **Step 4: Run storage tests**

Run: `go test ./internal/adapters/storage -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/storage/sqlite.go internal/adapters/storage/project_store.go internal/adapters/storage/project_fact_store.go internal/adapters/storage/project_fact_store_test.go
git commit -m "feat: persist structured project facts"
```

### Task 3: Replace runtime Blackboard ownership and Fact Index request assembly

**Files:**
- Modify: `internal/app/project_runtime.go`
- Modify: `internal/app/context_assembler.go`
- Modify: `internal/app/context_assembler_test.go`
- Modify: `internal/app/context_meter.go`
- Modify: `internal/app/context_meter_test.go`
- Modify: `internal/app/coordinator.go`

**Interfaces:**
- `ProjectRuntime.ProjectFacts() *storage.ProjectFactStore` returns the project-scoped ledger.
- `ContextAssembler.Prepare` renders `FactIndex` with `floor(ContextWindow * FactIndexRatio)` and returns `agent.ContextFactIndexLimited` when truncated.
- Disabled context replay includes no legacy Blackboard content and does not require a Fact Index read.

- [ ] **Step 1: Write failing runtime/assembler tests**

Test that a new runtime opens its fact store, enabled assembly injects `<project-fact-index ...>` rather than `<project-facts>`, fact-index truncation produces the new activity, deprecated facts are absent, and deterministic ordering follows pinned/category/updated/key. Test disabled context continues replaying only transcript/system instructions and does not open or render a fact index.

```go
func TestAssemblerInjectsBoundedStructuredFactIndex(t *testing.T) {
    fixture := newAssemblerFixture(t)
    defer fixture.close()
    mustUpsertFact(t, fixture.runtime.ProjectFacts(), pinnedTargetFact(), nil)
    input, activities, err := NewContextAssembler(fixture.runtime, enabledPolicy(), NewContextMeter(), nil).
        Prepare(context.Background(), fixture.session.ID, "system", nil)
    if err != nil || !strings.Contains(input.ProjectFacts, "<project-fact-index") {
        t.Fatalf("input/activities/error = %#v/%#v/%v", input, activities, err)
    }
}
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./internal/app -run 'Assembler|ProjectRuntime|FactIndex' -count=1`

Expected: FAIL because runtime still owns Blackboard.

- [ ] **Step 3: Implement runtime cutover**

Remove `blackboard *domain.Blackboard`, Blackboard getters, `UpdateBlackboard`, and Blackboard persistence from `ProjectRuntime`. Open one `ProjectFactStore` with the project runtime and close it during runtime shutdown. Replace all request names/comments from Blackboard to Fact Index. Render the index only in the enabled `ContextAssembler` path and feed its exact envelope to `ContextMeter`; preserve the no-fact message and explicit shown/omitted/truncated metadata. Add `ContextFactIndexLimited` to `agent.ContextActivity` constants and update callers.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/app -run 'Assembler|ProjectRuntime|FactIndex' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/project_runtime.go internal/app/context_assembler.go internal/app/context_assembler_test.go internal/app/context_meter.go internal/app/context_meter_test.go internal/app/coordinator.go internal/agent/context.go
git commit -m "feat: assemble bounded structured fact index"
```

### Task 4: Replace the legacy fact tool with six bounded host tools

**Files:**
- Modify: `internal/app/tools.go`
- Modify: `internal/app/tools_test.go` (create if absent)
- Modify: `internal/app/coordinator_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- `runtimeToolProvider.Tools` exposes the exact six Phase 2 fact tool names and does not expose `write_project_fact`.
- `upsert_project_fact` accepts `{key, category, summary, body, confidence, pinned?, evidence_refs?, edges?}`.
- `get_project_fact` accepts `{key}`; list/search accept bounded `limit`; deprecate/restore accept key and restore confidence.

- [ ] **Step 1: Write failing host-tool tests**

Cover the exact visible tool list, JSON schema required fields, confirmed fact with missing/failed evidence rejection, tentative evidence-free success, atomic edge write, bounded list/search result count, deprecated default exclusion, get full Body, restore rules, and absence of `write_project_fact`.

```go
func TestRuntimeToolsExposeOnlyStructuredFactTools(t *testing.T) {
    tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
    if err != nil { t.Fatal(err) }
    names := toolNames(tools)
    for _, want := range []string{"upsert_project_fact", "get_project_fact", "list_project_facts", "search_project_facts", "deprecate_project_fact", "restore_project_fact"} {
        if !names[want] { t.Fatalf("missing %s: %#v", want, names) }
    }
    if names["write_project_fact"] { t.Fatal("legacy fact writer remains exposed") }
}
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./internal/app -run 'FactTool|RuntimeTools' -count=1`

Expected: FAIL because `write_project_fact` remains exposed.

- [ ] **Step 3: Implement tools and configuration reservations**

Delete `writeProjectFactTool` and `blackboardText`. Add one focused tool struct per host capability or a typed fact-tool dispatcher with a `kind` enum; do not put argument validation in generic tool infrastructure. Use `stringValue`, strict `[]int`/`[]edge` decoders, maximum limits (`body <= 16 KiB`, `summary <= 2048 runes`, `list/search limit <= 100`, query <= 256 runes, edges <= 64), and storage validation. `Invoke` must return bounded user-readable result text; it never exposes secrets or raw SQL errors. Reserve all six names in `localToolReservedNames` and project-tool collision checks.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/app ./internal/config -run 'FactTool|RuntimeTools|Reserved' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/tools.go internal/app/tools_test.go internal/app/coordinator_test.go internal/config/config.go internal/config/config_test.go
git commit -m "feat: expose structured project fact tools"
```

### Task 5: Migrate CLI activity, documentation, and public configuration

**Files:**
- Modify: `internal/cli/model.go`
- Modify: `internal/cli/model_test.go`
- Modify: `README.md`
- Modify: `docs/TECHNICAL.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- CLI displays `ContextFactIndexLimited` as non-transcript context activity.
- User configuration docs contain `fact_index_ratio` and the six replacement tools; no documentation instructs users to use Blackboard or `write_project_fact`.

- [ ] **Step 1: Write failing CLI tests**

Add an event test for `ContextFactIndexLimited` that asserts a visible temporary context-activity line and no transcript insertion.

- [ ] **Step 2: Run focused CLI test and confirm failure**

Run: `go test ./internal/cli -run FactIndex -count=1`

Expected: FAIL because the activity kind is not rendered.

- [ ] **Step 3: Implement presentation and documentation**

Render Fact Index omission activity with shown/omitted counts. Replace README examples/configuration names and technical architecture descriptions with the structured ledger, graph edge semantics, evidence confidence constraints, deterministic index order, bounded host tools, v5 legacy migration, and explicit raw-ledger preservation.

- [ ] **Step 4: Run CLI and documentation checks**

Run: `go test ./internal/cli -count=1 && git diff --check`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/model.go internal/cli/model_test.go README.md docs/TECHNICAL.md docs/ARCHITECTURE.md
git commit -m "docs: describe structured project facts"
```

### Task 6: End-to-end migration and regression verification

**Files:**
- Modify: relevant Phase 2 test files only where coverage gaps remain.

- [ ] **Step 1: Add end-to-end migration test**

Create a v5 project with legacy facts, open through `Coordinator`, assert legacy writer is absent, structured facts are discoverable through `list_project_facts`, a confirmed upsert using a successful Evidence row appears in the enabled Fact Index, a deprecated fact does not, and session resume preserves the same index projection.

- [ ] **Step 2: Run the end-to-end test**

Run: `go test ./internal/app ./internal/adapters/storage -run 'Migration|StructuredFact|FactIndex' -count=1`

Expected: PASS.

- [ ] **Step 3: Run mandatory complete verification**

```bash
gofmt -w internal/domain/model.go internal/config/config.go internal/agent/context.go internal/adapters/storage/sqlite.go internal/adapters/storage/project_fact_store.go internal/app/project_runtime.go internal/app/context_assembler.go internal/app/context_meter.go internal/app/tools.go internal/cli/model.go
git diff --check
TMPDIR="$HOME/.cache/pentgo-go-tmp" GOTMPDIR="$HOME/.cache/pentgo-go-tmp" go test ./... -count=1
TMPDIR="$HOME/.cache/pentgo-go-tmp" GOTMPDIR="$HOME/.cache/pentgo-go-tmp" go test ./... -race -count=1
go vet ./...
go build ./...
GOOS=windows GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

Expected: every command exits 0.

- [ ] **Step 4: Review the complete diff**

Verify: there are no references to `domain.Blackboard`, `facts` table, `blackboard_ratio`, or `write_project_fact`; raw transcript/Evidence schema remains present and untouched; no provider adapter invokes fact storage/tools.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: migrate to structured project fact ledger"
```

## Plan Self-Review

- **Spec coverage:** Task 1 covers the new domain/config/context contracts. Task 2 covers normalized ledger/graph persistence, deterministic v5 migration, evidence constraints and atomic write rules. Task 3 replaces runtime Blackboard injection with bounded Fact Index assembly. Task 4 implements all six required host tools and removes the legacy tool. Task 5 covers UI/config/docs. Task 6 validates end-to-end migration and platform verification.
- **No-placeholder scan:** All tasks name exact files, public interfaces, storage tables, validation rules, tests, commands, and commit boundaries.
- **Type consistency:** `ProjectFactStore`, `ProjectFactWrite`, `FactQuery`, `FactIndex`, `FactIndexRatio`, and `ContextFactIndexLimited` are introduced before later tasks consume them. Tool names match the Phase 2 specification exactly.
