# SQLite Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace project JSON and JSONL persistence with a transactional SQLite database while preserving the storage adapter API used by the application.

**Architecture:** Model project facts as normalized relations in `pentgo.db`: projects, sessions, targets, turns, facts, findings, evidence references, evidence records, messages, and tool calls. Derive project session summaries and session finding IDs from those relations and commit cross-aggregate changes transactionally.

**Tech Stack:** Go `database/sql`, `modernc.org/sqlite`, existing domain JSON encodings, Go tests.

## Global Constraints

- Keep application-facing storage behavior stable.
- Use one SQLite database per project at `<project-root>/pentgo.db`.
- Use transactions for multi-record commits.

---

### Task 1: SQLite project store

**Files:**
- Create: `internal/adapters/storage/sqlite.go`
- Modify: `internal/adapters/storage/project_store.go`
- Test: `internal/adapters/storage/project_store_test.go`

**Interfaces:**
- Consumes: existing `domain.Project`, `domain.Session`, and `domain.Blackboard` types.
- Produces: `ProjectStore.DatabasePath() string` and transactional implementations of existing project store methods.

- [ ] **Step 1: Add tests for database creation, reopen, derived session summaries, deletion, and concurrent writes**

Run: `go test ./internal/adapters/storage -run 'TestProject|TestSession|TestConcurrent'`

Expected: FAIL before the SQLite implementation and PASS after it.

- [ ] **Step 2: Add the SQLite driver and schema initialization**

Run: `go get modernc.org/sqlite && go mod tidy`

Expected: `modernc.org/sqlite` appears as a direct dependency.

- [ ] **Step 3: Replace JSON project/session/blackboard operations with SQL queries and transactions**

Run: `go test ./internal/adapters/storage`

Expected: PASS.

### Task 2: SQLite evidence and transcripts

**Files:**
- Modify: `internal/adapters/storage/evidence_store.go`
- Modify: `internal/adapters/storage/transcript_store.go`
- Test: `internal/adapters/storage/evidence_store_test.go`
- Test: `internal/adapters/storage/transcript_store_test.go`

**Interfaces:**
- Consumes: `Record`, `agent.Message`, and the project database path.
- Produces: durable ordered evidence and transcript rows with the existing append, lookup, messages, path, and close methods.

- [ ] **Step 1: Rewrite persistence tests to reopen SQLite stores and verify ordering, redaction, concurrency, and close behavior**

Run: `go test ./internal/adapters/storage -run 'TestEvidence|TestTranscript'`

Expected: FAIL before implementation and PASS after it.

- [ ] **Step 2: Implement evidence inserts/lookups and transcript inserts/loads with SQL**

Run: `go test ./internal/adapters/storage`

Expected: PASS.

### Task 3: Runtime lifecycle and documentation

**Files:**
- Modify: `internal/app/project_runtime.go`
- Modify: `cmd/pentgo/main_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Consumes: `ProjectStore.Close()` and `ProjectStore.DatabasePath()`.
- Produces: deterministic database handle shutdown and current persistence documentation.

- [ ] **Step 1: Close the project store after transcripts and evidence on runtime shutdown**

Run: `go test ./internal/app ./cmd/pentgo`

Expected: PASS.

- [ ] **Step 2: Update artifact assertions and documentation to `pentgo.db`**

Run: `rg -n 'pentgo\\.db' README.md docs/ARCHITECTURE.md internal cmd`

Expected: current persistence documentation and tests refer to `pentgo.db`.

- [ ] **Step 3: Run repository verification**

Run: `gofmt -w internal/adapters/storage/*.go internal/app/project_runtime.go cmd/pentgo/main_test.go && go test ./... && go vet ./...`

Expected: all commands pass.
