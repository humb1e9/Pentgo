# Eino MCP Evidence Slimming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace PentGo's legacy text, completion-gate, verification, and report-model paths with one naturally terminating Eino agent whose local and MCP actions append to one `evidence.jsonl`, whose findings are explicitly recorded, and whose terminal artifacts are rendered locally.

**Architecture:** One Eino `ChatModelAgent` owns the engagement loop for both providers. Local `exec`/`execute_python` adapters and one optional local stdio MCP client share a concrete engagement-local Evidence Journal; `record_finding` only performs mechanical reference validation, and the first assistant response without tool calls becomes `final_summary`. The application closes action resources, removes only registered Runtime scripts, renders `session.json` and `report.md`, and atomically publishes the engagement.

**Tech Stack:** Go 1.25, CloudWeGo Eino/ADK v0.9.13, official `github.com/modelcontextprotocol/go-sdk` v1.7.0, standard-library JSONL/file/process primitives, existing PentGo preflight and authorization code.

## Global Constraints

- Treat every target, credential, payload, and response as a synthetic local CTF fixture.
- Network-facing tests use local `httptest` servers and local stdio fixture processes only.
- Support exactly one optional local stdio MCP server per engagement.
- Use `exec(command)` with Bash and `execute_python(script)` with `python3 -u`; remove the public `execute_code` alias.
- Persist exactly `evidence.jsonl`, `session.json`, `report.md`, and `work/` in each published engagement.
- Record `exec`, `execute_python`, and discovered MCP tools; omit `load_skill` and `record_finding` from `evidence.jsonl`.
- A normal assistant response without tool calls is the only successful completion signal.
- Remove `complete_task`, `evidence_gate`, `declare_session`, verification/replay, evidence grading, model-generated reports, text clients, and text/code-fence loop compatibility.
- Keep preflight repair/rejection, authorization and target scope, execution timeout/cancellation, output caps, repeated-output protection, Eino maximum iterations, and action-tool network backoff.
- Use a concrete file Journal and concrete stdio MCP client; add no database, storage interface, manager, monitoring, RBAC, HITL, reconnect, multiple-server, HTTP/SSE, resource, prompt, sampling, or multimodal layer.
- This plan supersedes `docs/superpowers/plans/2026-07-29-minimal-mcp-execution-tools.md`; the two plans must not be executed independently.
- The previously discussed vulnerability-revalidation test 10 is deleted with the verifier. Deterministic report rendering remains covered, but no replacement replay test is added.

---

## File Map

**Create**

- `internal/runtime/evidence/journal.go` - concrete compact JSONL append, completion-order sequence allocation, redaction, and successful-reference lookup.
- `internal/runtime/evidence/journal_test.go` - exact-line, empty-file, concurrent append, reference lookup, redaction, and write-failure tests.
- `internal/runtime/mcp/client.go` - one stdio subprocess, discovery, Eino schema adapter, tool invocation, normalized bounded result, and Journal recording.
- `internal/runtime/mcp/client_test.go` - local helper-process MCP fixture and discovery/success/error/malformed-argument lifecycle tests.
- `internal/app/engagement_mcp_test.go` - full MCP tool to finding to natural-final-response publication test.
- `internal/agent/eino_model_test.go` - retained Eino provider-construction helper coverage after deleting text-client tests.

**Replace or materially rewrite**

- `internal/runtime/exec/executor.go`, `internal/runtime/exec/executor_test.go` - remove per-block evidence/session environment behavior and register exact Runtime script paths for cleanup.
- `internal/runtime/exec/blocks.go`, `internal/runtime/exec/blocks_test.go` - retain only `Language` and `CodeBlock`; remove fenced Markdown parsing.
- `internal/runtime/session/session.go`, `internal/runtime/session/session_test.go` - compact terminal state plus Agent-recorded findings and final summary.
- `internal/runtime/loop/runner.go`, `internal/runtime/loop/runner_test.go` - small Eino-only runner configuration, events, shared rendering helpers, and no text client.
- `internal/runtime/loop/eino_agent.go` - `exec`, `execute_python`, `load_skill`, `record_finding`, external MCP tool merge, and plain ADK agent construction.
- `internal/runtime/loop/eino_run_loop.go`, `internal/runtime/loop/eino_run_loop_test.go` - one ADK run, natural completion, and terminal-state mapping.
- `internal/runtime/loop/prompt.go`, `internal/runtime/loop/prompt_content_test.go`, `internal/runtime/loop/prompt_test.go` - CAI-style tool choice, finding recording, and natural stopping instructions.
- `internal/report/artifacts.go`, `internal/report/artifacts_test.go`, `internal/report/markdown.go` - root JSONL path, compact artifacts, deterministic report, and atomic publication.
- `internal/config/config.go`, `internal/config/config_test.go` - remove dead recovery/verification fields and add optional MCP stdio config.
- `internal/agent/types.go`, `internal/agent/eino_model.go` - retain only Eino provider types/helpers and correct provider comments.
- `internal/app/engagement.go`, `internal/app/engagement_test.go` - Eino-only orchestration, close/cleanup/publish ordering, and local end-to-end coverage.
- `cmd/pentgo/main_test.go` - three-turn tool/finding/natural-summary fixture and new artifact assertions.
- `internal/terminal/terminal.go`, `internal/terminal/terminal_test.go` - adapt compact session field names and retained progress events.
- `README.md`, `docs/ARCHITECTURE.md` - document the final minimal surface and artifact contract.
- `go.mod`, `go.sum` - add official MCP SDK v1.7.0 and remove dependencies made unreachable by deleted code.

**Delete**

- `internal/agent/openai.go`
- `internal/agent/anthropic.go`
- `internal/agent/client_test.go`
- `internal/report/findings.go`
- `internal/report/generator.go`
- `internal/report/generator_test.go`
- `internal/runtime/exec/evidence_grade.go`
- `internal/runtime/exec/evidence_grade_test.go`
- `internal/runtime/loop/finding_label.go`
- `internal/runtime/loop/finding_label_test.go`
- `internal/runtime/loop/history.go`
- `internal/runtime/loop/history_test.go`
- `internal/runtime/loop/refusal.go`
- `internal/runtime/loop/refusal_test.go`
- `internal/runtime/loop/report_context.go`
- `internal/runtime/loop/report_context_test.go`
- `internal/runtime/loop/session_block.go`
- `internal/runtime/loop/session_block_test.go`
- `internal/runtime/loop/session_runtime.go`
- `internal/runtime/loop/validation.go`
- `internal/runtime/loop/validation_test.go`
- `internal/runtime/session/pool.go`
- `internal/runtime/session/pool_test.go`
- `internal/runtime/verify/csrf.go`
- `internal/runtime/verify/csrf_test.go`
- `internal/runtime/verify/finding_spec.go`
- `internal/runtime/verify/finding_spec_test.go`
- `internal/runtime/verify/http_verifier.go`
- `internal/runtime/verify/http_verifier_test.go`
- `internal/runtime/verify/privesc_test.go`
- `internal/runtime/verify/privesc_http_test.go`
- `internal/runtime/verify/verification.go`
- `internal/runtime/verify/verification_test.go`

---

### Task 1: Add the Concrete Evidence Journal

**Files:**
- Create: `internal/runtime/evidence/journal.go`
- Create: `internal/runtime/evidence/journal_test.go`

**Interfaces:**
- Consumes: a staging path, configured secret values, already bounded action output, and UTC start/finish times.
- Produces: `NewJournal(path string, secrets ...string) (*Journal, error)`, `(*Journal).Record(tool string, arguments any, success bool, output string, startedAt, finishedAt time.Time) (Record, error)`, `(*Journal).Lookup(seq int) (Record, bool)`, `(*Journal).Path() string`, and `(*Journal).Close() error`.

- [ ] **Step 1: Write the failing tests for file shape, references, redaction, and an empty engagement**

Create `internal/runtime/evidence/journal_test.go` with these focused cases:

```go
package evidence

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJournalCreatesEmptyFileAndAppendsOneCompactLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	journal, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("new journal = %q, want empty", before)
	}
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)
	record, err := journal.Record("execute_python", map[string]any{"script": "print('RESULT')"}, true, "RESULT", started, finished)
	if err != nil {
		t.Fatal(err)
	}
	if record.Seq != 1 || record.Output != "RESULT\n[evidence_ref: 1]" {
		t.Fatalf("record = %+v", record)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("journal must contain one physical JSONL line: %q", data)
	}
	var decoded Record
	if err := json.Unmarshal(data[:len(data)-1], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Output != record.Output || decoded.Tool != "execute_python" || !decoded.Success {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestJournalRedactsConfiguredSecretsAndIndexesSuccess(t *testing.T) {
	journal, err := NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"), "TOKEN-VALUE")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	record, err := journal.Record("fixture_echo", map[string]any{"value": "TARGET"}, true, "TOKEN-VALUE", time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record.Output, "TOKEN-VALUE") || !strings.Contains(record.Output, "[redacted]") {
		t.Fatalf("output = %q", record.Output)
	}
	stored, ok := journal.Lookup(record.Seq)
	if !ok || !stored.Success || stored.Output != record.Output {
		t.Fatalf("lookup = %+v, %v", stored, ok)
	}
	if _, ok := journal.Lookup(99); ok {
		t.Fatal("unexpected missing reference")
	}
}

func TestJournalConcurrentAppendsUseCompletionOrderSequenceAndValidLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.jsonl")
	journal, err := NewJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	const calls = 32
	var wait sync.WaitGroup
	wait.Add(calls)
	for index := 0; index < calls; index++ {
		go func(index int) {
			defer wait.Done()
			_, recordErr := journal.Record("exec", map[string]any{"command": index}, index%2 == 0, "RESULT", time.Now().UTC(), time.Now().UTC())
			if recordErr != nil {
				t.Errorf("record %d: %v", index, recordErr)
			}
		}(index)
	}
	wait.Wait()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	seen := make(map[int]bool, calls)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		seen[record.Seq] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= calls; seq++ {
		if !seen[seq] {
			t.Fatalf("missing sequence %d", seq)
		}
	}
}

func TestJournalFailureIsStickyAndClassified(t *testing.T) {
	journal, err := NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.file.Close(); err != nil {
		t.Fatal(err)
	}
	_, first := journal.Record("exec", map[string]any{"command": "true"}, true, "ok", time.Now().UTC(), time.Now().UTC())
	_, second := journal.Record("exec", map[string]any{"command": "true"}, true, "ok", time.Now().UTC(), time.Now().UTC())
	if !errors.Is(first, ErrWrite) || !errors.Is(second, ErrWrite) {
		t.Fatalf("errors = %v, %v", first, second)
	}
}
```

- [ ] **Step 2: Run the Journal tests and verify the package is missing**

Run:

```bash
go test ./internal/runtime/evidence -count=1
```

Expected: FAIL because `internal/runtime/evidence` and `NewJournal` do not exist.

- [ ] **Step 3: Implement the concrete Journal and compact record schema**

Create `internal/runtime/evidence/journal.go` with this public surface and behavior:

```go
package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrWrite = errors.New("write evidence journal")

type Record struct {
	Seq        int       `json:"seq"`
	Tool       string    `json:"tool"`
	Arguments  any       `json:"arguments"`
	Success    bool      `json:"success"`
	Output     string    `json:"output"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Journal struct {
	mu      sync.Mutex
	file    *os.File
	path    string
	next    int
	records map[int]Record
	secrets []string
	failed  error
	closed  bool
}

func NewJournal(path string, secrets ...string) (*Journal, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("evidence path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create evidence directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create evidence journal: %w", err)
	}
	return &Journal{
		file:    file,
		path:    path,
		records: make(map[int]Record),
		secrets: normalizeSecrets(secrets),
	}, nil
}

func (journal *Journal) Record(tool string, arguments any, success bool, output string, startedAt, finishedAt time.Time) (Record, error) {
	if journal == nil {
		return Record{}, fmt.Errorf("%w: nil journal", ErrWrite)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.failed != nil {
		return Record{}, journal.failed
	}
	if journal.closed {
		journal.failed = fmt.Errorf("%w: journal closed", ErrWrite)
		return Record{}, journal.failed
	}
	seq := journal.next + 1
	output = strings.TrimRight(journal.redact(output), "\n")
	if output != "" {
		output += "\n"
	}
	output += fmt.Sprintf("[evidence_ref: %d]", seq)
	record := Record{Seq: seq, Tool: tool, Arguments: arguments, Success: success, Output: output, StartedAt: startedAt, FinishedAt: finishedAt}
	data, err := json.Marshal(record)
	if err == nil {
		_, err = journal.file.Write(append(data, '\n'))
	}
	if err == nil {
		err = journal.file.Sync()
	}
	if err != nil {
		journal.failed = fmt.Errorf("%w: %v", ErrWrite, err)
		return Record{}, journal.failed
	}
	journal.next = seq
	journal.records[seq] = record
	return record, nil
}

func (journal *Journal) Lookup(seq int) (Record, bool) {
	if journal == nil {
		return Record{}, false
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	record, ok := journal.records[seq]
	return record, ok
}

func (journal *Journal) Path() string {
	if journal == nil {
		return ""
	}
	return journal.path
}

func (journal *Journal) Close() error {
	if journal == nil {
		return nil
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return journal.failed
	}
	journal.closed = true
	if err := journal.file.Close(); err != nil && journal.failed == nil {
		journal.failed = fmt.Errorf("%w: close: %v", ErrWrite, err)
	}
	return journal.failed
}

func normalizeSecrets(values []string) []string {
	seen := make(map[string]bool)
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func (journal *Journal) redact(value string) string {
	for _, secret := range journal.secrets {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}
```

The mutex surrounds sequence allocation, compact marshaling, append, `Sync`, and index insertion. A sequence becomes visible only after its physical line is durable.

- [ ] **Step 4: Run the Journal tests**

Run:

```bash
go test ./internal/runtime/evidence -count=1
go test -race ./internal/runtime/evidence -count=1
```

Expected: PASS; the race run reports no races.

- [ ] **Step 5: Commit the Journal**

```bash
git add internal/runtime/evidence/journal.go internal/runtime/evidence/journal_test.go
git commit -m "feat: add engagement evidence journal"
```

---

### Task 2: Register and Remove Only Runtime-Generated Scripts

**Files:**
- Modify: `internal/runtime/exec/executor.go`
- Modify: `internal/runtime/exec/executor_test.go`

**Interfaces:**
- Consumes: the existing concrete `*exec.Executor` and each successfully written `turn-*.py` or `turn-*.sh` path.
- Produces: `(*Executor).CleanupGeneratedScripts() error`; no glob or directory-wide removal.

- [ ] **Step 1: Write failing exact-path cleanup tests**

Append these tests to `internal/runtime/exec/executor_test.go`:

```go
func TestCleanupGeneratedScriptsRemovesRuntimeFilesAndPreservesAgentFiles(t *testing.T) {
	workDir := t.TempDir()
	executor := NewExecutor(ExecutorConfig{WorkDir: workDir})
	results := executor.Execute(context.Background(), ExecutionInput{
		SessionID: "eng-ID",
		Target:    "TARGET",
		Turn:      1,
		Blocks: []PreflightResult{{
			Block:    CodeBlock{Index: 1, Language: LanguagePython, Code: "open('artifact.txt', 'w').write('keep')"},
			Code:     "open('artifact.txt', 'w').write('keep')",
			Approved: true,
		}},
	})
	if len(results) != 1 || results[0].Status != ExecutionSucceeded {
		t.Fatalf("results = %+v", results)
	}
	if err := executor.CleanupGeneratedScripts(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(results[0].ScriptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime script stat error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workDir, "artifact.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("agent artifact = %q, %v", data, err)
	}
}

func TestCleanupGeneratedScriptsUsesRegisteredPathsInsteadOfGlob(t *testing.T) {
	workDir := t.TempDir()
	keep := filepath.Join(workDir, "turn-999-block-999.py")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(ExecutorConfig{WorkDir: workDir})
	if err := executor.CleanupGeneratedScripts(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatalf("unregistered file = %q, %v", data, err)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the method is missing**

Run:

```bash
go test ./internal/runtime/exec -run 'TestCleanupGeneratedScripts' -count=1
```

Expected: FAIL because `CleanupGeneratedScripts` does not exist.

- [ ] **Step 3: Add the registry to the concrete Executor**

Add these fields and methods in `internal/runtime/exec/executor.go`:

```go
type Executor struct {
	config           ExecutorConfig
	generatedMu      sync.Mutex
	generatedScripts []string
}

func (executor *Executor) registerGeneratedScript(path string) {
	executor.generatedMu.Lock()
	defer executor.generatedMu.Unlock()
	executor.generatedScripts = append(executor.generatedScripts, path)
}

func (executor *Executor) CleanupGeneratedScripts() error {
	if executor == nil {
		return nil
	}
	executor.generatedMu.Lock()
	defer executor.generatedMu.Unlock()
	for _, path := range executor.generatedScripts {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Runtime script %s: %w", path, err)
		}
	}
	executor.generatedScripts = nil
	return nil
}
```

Add `errors` to the imports. In `writeScript`, register only after `os.WriteFile` succeeds:

```go
	if err := os.WriteFile(path, []byte(preflight.Code), 0o600); err != nil {
		return "", "", nil, fmt.Errorf("write script: %w", err)
	}
	executor.registerGeneratedScript(path)
	arguments = append(arguments, path)
```

- [ ] **Step 4: Run executor tests and race checks**

Run:

```bash
go test ./internal/runtime/exec -count=1
go test -race ./internal/runtime/exec -run 'TestCleanupGeneratedScripts|TestExecutorRunsPythonAndShellInSourceOrder' -count=1
```

Expected: PASS; existing execution behavior is unchanged and cleanup has no race.

- [ ] **Step 5: Commit exact-path script cleanup**

```bash
git add internal/runtime/exec/executor.go internal/runtime/exec/executor_test.go
git commit -m "feat: clean registered runtime scripts"
```

---

### Task 3: Replace the Legacy Runtime With the Minimal Natural-Completion Core

This is one compile-boundary task: the current session, runner, report, application, and text-client types form a cycle of old assumptions. Replacing only one package would leave the repository uncompilable, so the deletion and the new Eino-only path land together in one reviewer gate.

**Files:**
- Modify: `internal/runtime/exec/executor.go`
- Modify: `internal/runtime/exec/executor_test.go`
- Modify: `internal/runtime/exec/blocks.go`
- Modify: `internal/runtime/exec/blocks_test.go`
- Modify: `internal/runtime/session/session.go`
- Modify: `internal/runtime/session/session_test.go`
- Modify: `internal/runtime/loop/runner.go`
- Modify: `internal/runtime/loop/runner_test.go`
- Modify: `internal/runtime/loop/eino_agent.go`
- Modify: `internal/runtime/loop/eino_run_loop.go`
- Modify: `internal/runtime/loop/eino_run_loop_test.go`
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_content_test.go`
- Modify: `internal/runtime/loop/prompt_test.go`
- Modify: `internal/report/artifacts.go`
- Modify: `internal/report/artifacts_test.go`
- Modify: `internal/report/markdown.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/agent/types.go`
- Modify: `internal/agent/eino_model.go`
- Create: `internal/agent/eino_model_test.go`
- Modify: `internal/app/engagement.go`
- Modify: `internal/app/engagement_test.go`
- Modify: `internal/terminal/terminal.go`
- Modify: `internal/terminal/terminal_test.go`
- Modify: `cmd/pentgo/main_test.go`
- Delete: every legacy file listed in the File Map's **Delete** section.

**Interfaces:**
- Consumes: `evidence.Journal`, current `authz.Authorizer`, `authz.Scope`, `exec.Preflight`, `BlockExecutor.Execute`, Eino `model.ToolCallingChatModel`, Skills catalog/loader, and the engagement writer's staging directory.
- Produces: compact `session.AgentSession`, Eino tools `exec`, `execute_python`, `load_skill`, and `record_finding`; `loop.NewRunner(executor BlockExecutor, journal *evidence.Journal, config RunnerConfig, load SkillLoader, sleep Sleeper) *Runner`; `(*Runner).RunEino(context.Context, *session.AgentSession, model.ToolCallingChatModel) error`; deterministic `EngagementWriter.Publish`; and an Eino-only `app.Service`.

- [ ] **Step 1: Replace session tests with the compact lifecycle and finding schema**

Replace `internal/runtime/session/session_test.go` with:

```go
package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionTracksCompactLifecycleAndFinalSummary(t *testing.T) {
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	session := NewSession(Target{Canonical: "TARGET"}, "TASK", started)
	if err := session.Start(started); err != nil {
		t.Fatal(err)
	}
	session.Turns = 3
	session.Findings = append(session.Findings, Finding{
		Title:          "Fixture finding",
		Severity:       "high",
		Description:    "Observed RESULT.",
		EvidenceRefs:   []int{1},
		Recommendation: "Apply fixture control.",
	})
	session.FinalSummary = "Completed fixture task."
	if err := session.Complete("agent_finished", started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{`"target":"TARGET"`, `"turns":3`, `"final_summary":"Completed fixture task."`, `"evidence_refs":[1]`} {
		if !strings.Contains(text, required) {
			t.Fatalf("session JSON missing %s: %s", required, text)
		}
	}
	for _, removed := range []string{"timeline", "loaded_skills", "sessions", "confidence", "verdict"} {
		if strings.Contains(text, removed) {
			t.Fatalf("session JSON contains removed field %q: %s", removed, text)
		}
	}
}

func TestSessionRejectsInvalidLifecycleTransition(t *testing.T) {
	session := NewSession(Target{Canonical: "TARGET"}, "TASK", time.Now().UTC())
	if err := session.Complete("agent_finished", time.Now().UTC()); err == nil {
		t.Fatal("pending session completed")
	}
}
```

- [ ] **Step 2: Replace report tests with exact compact artifact and ordering assertions**

Rewrite `internal/report/artifacts_test.go` to cover these cases with a started and completed compact session:

```go
func artifactTestSession(t *testing.T) *sess.AgentSession {
	t.Helper()
	started := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	session := sess.NewSession(sess.Target{Canonical: "TARGET"}, "TASK", started)
	session.ID = "eng-ID"
	if err := session.Start(started); err != nil {
		t.Fatal(err)
	}
	if err := session.Complete("agent_finished", started.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestEngagementWriterPublishesJournalSessionReportAndWork(t *testing.T) {
	root := t.TempDir()
	session := artifactTestSession(t)
	session.Findings = []sess.Finding{{
		Title: "Fixture finding", Severity: "high", Description: "Description.",
		EvidenceRefs: []int{1, 2}, Recommendation: "Recommendation.",
	}}
	session.FinalSummary = "Agent summary."
	writer, err := NewEngagementWriter(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	if err := os.WriteFile(writer.EvidencePath(), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.WorkDir(), "artifact.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts, err := writer.Publish(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{artifacts.EvidenceJSONL, artifacts.SessionJSON, artifacts.Markdown, filepath.Join(artifacts.WorkDirectory, "artifact.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	markdown, err := os.ReadFile(artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	findingAt := strings.Index(string(markdown), "### [HIGH] Fixture finding")
	summaryAt := strings.Index(string(markdown), "## Agent Summary")
	if findingAt < 0 || summaryAt < 0 || findingAt >= summaryAt {
		t.Fatalf("report order is wrong:\n%s", markdown)
	}
	if !strings.Contains(string(markdown), "Evidence: `#1`, `#2`") {
		t.Fatalf("missing references:\n%s", markdown)
	}
}

func TestReportStatesWhenNoFindingsWereRecorded(t *testing.T) {
	session := artifactTestSession(t)
	markdown := renderMarkdown(session)
	if !strings.Contains(markdown, "No findings were recorded.") {
		t.Fatalf("report = %s", markdown)
	}
}
```

Keep the existing invalid engagement ID, existing destination, cancelled-session publication, and atomic publication assertions, adapting `Target.Canonical` to `Target` and `Turn` to `Turns`. Delete every test that expects a model report or per-block evidence JSON.

- [ ] **Step 3: Replace loop tests with natural completion, action journaling, finding validation, and terminal errors**

Rewrite `internal/runtime/loop/eino_run_loop_test.go` and `internal/runtime/loop/runner_test.go` around the existing fake `ToolCallingChatModel`. The scripted success sequence is exactly:

```go
type blockExecutorFunc func(context.Context, exec.ExecutionInput) []exec.ExecutionResult

func (execute blockExecutorFunc) Execute(ctx context.Context, input exec.ExecutionInput) []exec.ExecutionResult {
	return execute(ctx, input)
}

func successExecutor() BlockExecutor {
	return blockExecutorFunc(func(_ context.Context, input exec.ExecutionInput) []exec.ExecutionResult {
		now := time.Now().UTC()
		return []exec.ExecutionResult{{
			Block: input.Blocks[0].Block, Status: exec.ExecutionSucceeded, ExitCode: 0,
			Stdout: "RESULT\n", StartedAt: now, FinishedAt: now,
		}}
	})
}

func newTestJournal(t *testing.T) *evidence.Journal {
	t.Helper()
	journal, err := evidence.NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal
}

func runningTestSession(t *testing.T) *sess.AgentSession {
	t.Helper()
	session := sess.NewSession(sess.Target{Canonical: "https://fixture.local"}, "TASK", time.Now().UTC())
	if err := session.Start(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return session
}

func noSleep(context.Context, time.Duration) error { return nil }

func einoTestConfig() RunnerConfig {
	return RunnerConfig{MaxTurns: 10, Authorizer: authz.NewAuthorizer(true), AllowPrivateHosts: true}
}

func successfulTurns() []*schema.Message {
	return []*schema.Message{
		toolCallMessage("call_exec", "execute_python", `{"script":"value = 'RESULT'\nprint(value)"}`),
		toolCallMessage("call_finding", "record_finding", `{"title":"Fixture finding","severity":"high","description":"Observed RESULT.","evidence_refs":[1],"recommendation":"Apply fixture control."}`),
		schema.AssistantMessage("Completed fixture task.", nil),
	}
}

func newTestToolSet(t *testing.T, journal *evidence.Journal) *einoToolSet {
	t.Helper()
	return &einoToolSet{
		executor: successExecutor(), journal: journal, session: runningTestSession(t),
		authorizer: authz.NewAuthorizer(true), scope: authz.NewScope("fixture.local", nil, true),
		load: func(string) (string, error) { return "fixture skill", nil },
		sleep: noSleep, loaded: make(map[string]bool),
	}
}
```

Add these named cases and exact assertions:

```go
func TestRunEinoRecordsActionFindingAndNaturalFinalResponse(t *testing.T) {
	journal := newTestJournal(t)
	session := runningTestSession(t)
	runner := NewRunner(successExecutor(), journal, einoTestConfig(), nil, noSleep)
	if err := runner.RunEino(context.Background(), session, &scriptedToolModel{turns: successfulTurns()}); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone || session.StopReason != "agent_finished" {
		t.Fatalf("terminal state = %s/%s", session.Status, session.StopReason)
	}
	if session.FinalSummary != "Completed fixture task." || len(session.Findings) != 1 || session.Findings[0].EvidenceRefs[0] != 1 {
		t.Fatalf("session = %+v", session)
	}
	record, ok := journal.Lookup(1)
	if !ok || !record.Success || record.Tool != "execute_python" || !strings.Contains(record.Output, "[evidence_ref: 1]") {
		t.Fatalf("record = %+v, %v", record, ok)
	}
}

func TestRunEinoAllowsDirectNaturalFinalResponse(t *testing.T) {
	journal := newTestJournal(t)
	session := runningTestSession(t)
	runner := NewRunner(successExecutor(), journal, einoTestConfig(), nil, noSleep)
	err := runner.RunEino(context.Background(), session, &scriptedToolModel{turns: []*schema.Message{schema.AssistantMessage("No findings.", nil)}})
	if err != nil || session.Status != sess.SessionDone || session.FinalSummary != "No findings." || len(session.Findings) != 0 {
		t.Fatalf("session = %+v, err = %v", session, err)
	}
}

func TestRecordFindingValidationIsSoftAndDoesNotWriteEvidence(t *testing.T) {
	journal := newTestJournal(t)
	_, err := journal.Record("exec", map[string]any{"command": "false"}, false, "failed", time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	tools := newTestToolSet(t, journal)
	for _, test := range []struct {
		name string
		args recordFindingArgs
		want string
	}{
		{"missing title", recordFindingArgs{Severity: "high", Description: "D", EvidenceRefs: []int{1}, Recommendation: "R"}, "title is required"},
		{"invalid severity", recordFindingArgs{Title: "T", Severity: "urgent", Description: "D", EvidenceRefs: []int{1}, Recommendation: "R"}, "severity must be one of"},
		{"missing description", recordFindingArgs{Title: "T", Severity: "high", EvidenceRefs: []int{1}, Recommendation: "R"}, "description is required"},
		{"missing recommendation", recordFindingArgs{Title: "T", Severity: "high", Description: "D", EvidenceRefs: []int{1}}, "recommendation is required"},
		{"empty refs", recordFindingArgs{Title: "T", Severity: "high", Description: "D", Recommendation: "R"}, "evidence_refs must contain at least one reference"},
		{"missing ref", recordFindingArgs{Title: "T", Severity: "high", Description: "D", EvidenceRefs: []int{99}, Recommendation: "R"}, "evidence_ref 99 does not exist"},
		{"failed ref", recordFindingArgs{Title: "T", Severity: "high", Description: "D", EvidenceRefs: []int{1}, Recommendation: "R"}, "evidence_ref 1 is not successful"},
		{"duplicate refs", recordFindingArgs{Title: "T", Severity: "high", Description: "D", EvidenceRefs: []int{1, 1}, Recommendation: "R"}, "duplicate evidence_ref 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, callErr := tools.recordFinding(context.Background(), test.args)
			if callErr != nil || !strings.Contains(got, test.want) {
				t.Fatalf("result = %q, err = %v", got, callErr)
			}
		})
	}
}
```

Also add:

- `TestRecordFindingAcceptsSuccessfulRefsAndRejectsCaseFoldedDuplicateTitle`: record one successful Journal action; the first finding returns `finding #1 recorded`; a second title `" fixture FINDING "` returns a soft duplicate-title message; `evidence.jsonl` still has one line.
- `TestRunEinoEmptyNaturalResponseFails`: status `failed`, stop reason `empty_response`, nil run error.
- `TestRunEinoMaxIterationsFails`: set `MaxTurns: 1`, script two consecutive tool-call responses, assert `failed/max_iterations` and nil run error.
- `TestRunEinoCancellationCancelsSession`: cancel the context used by the fake model, assert `cancelled/cancelled`.
- `TestRunEinoProviderErrorFailsSession`: fake model returns `errors.New("provider fixture")`, assert `failed/provider_error` and the returned error.
- `TestRunEinoJournalFailureStopsEngagement`: close the Journal's file before an `execute_python` call, assert `errors.Is(err, evidence.ErrWrite)` and `failed/evidence_error`.
- `TestLocalActionJournalStatusMapping`: run table cases for `ExecutionSucceeded`, `ExecutionFailed`, `ExecutionTimedOut`, `ExecutionCancelled`, `ExecutionRepeatedOutput`, and `ExecutionPreflightRejected`; assert only `ExecutionSucceeded` records `success:true`, every returned string equals the stored `Record.Output`, and sequences advance once per completed call.
- `TestBuildToolsRejectsExternalNameCollision`: an external tool named `exec` produces `external tool name collision: exec`.

Delete old tests for refusal recovery, no-code recovery, stuck fingerprints, completion gates, session declaration/cookies, finding consolidation, evidence grades, report context, and verification. This includes the old vulnerability-revalidation test 10; it receives no replacement.

- [ ] **Step 4: Run the replacement tests and capture the expected compile failures**

Run:

```bash
go test ./internal/runtime/session ./internal/report ./internal/runtime/loop ./internal/app ./cmd/pentgo -count=1
```

Expected: FAIL with missing `Finding`, `FinalSummary`, `EvidencePath`, `record_finding`, and the new `NewRunner` signature, plus references to old verification/text-client types.

- [ ] **Step 5: Replace the session domain with the compact terminal document**

Replace `internal/runtime/session/session.go` with this shape while retaining the existing `newSessionID` implementation:

```go
type Finding struct {
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Description    string `json:"description"`
	EvidenceRefs   []int  `json:"evidence_refs"`
	Recommendation string `json:"recommendation"`
}

type AgentSession struct {
	ID           string        `json:"id"`
	Target       string        `json:"target"`
	Intent       string        `json:"intent"`
	Status       SessionStatus `json:"status"`
	StopReason   string        `json:"stop_reason,omitempty"`
	Turns        int           `json:"turns"`
	StartedAt    time.Time     `json:"started_at"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	Findings     []Finding     `json:"findings"`
	FinalSummary string        `json:"final_summary"`
}

func NewSession(target Target, intent string, startedAt time.Time) *AgentSession {
	return &AgentSession{
		ID:        newSessionID(),
		Target:    target.Canonical,
		Intent:    intent,
		Status:    SessionPending,
		StartedAt: startedAt,
		Findings:  make([]Finding, 0),
	}
}
```

Retain `Start`, `Complete`, `Fail`, `Cancel`, and the guarded `finish` transition. Remove `TimelineEvent`, `AddEvent`, `LoadedSkills`, login sessions, and the `verify` import.

- [ ] **Step 6: Strip executor-owned evidence, grading, Cookie environment, and fenced parsing**

In `internal/runtime/exec/executor.go`:

- Remove `ExecutorConfig.Evidence`, `EvidenceSink`, `ExecutorConfig.MaxParallel`, `ExecutionInput.ExtraEnv`, `ExecutionResult.EvidencePath`, `ExecutionResult.Level`, `executionEvidence`, `persistEvidence`, and `RedactSessionSecrets`.
- Make `Execute` iterate over `input.Blocks` and call `executeBlock` directly; Eino's ToolsNode owns concurrency across distinct action calls.
- Keep the generated-script registry from Task 2.
- Keep `PENTGO_TARGET`, `PENTGO_ENGAGEMENT_ID`, and `PENTGO_WORKDIR`; remove the `ExtraEnv` append loop.
- Remove the deferred `GradeEvidence` assignment while retaining `FinishedAt`.

The resulting input/config/result surfaces are:

```go
type ExecutorConfig struct {
	WorkDir             string
	Timeout             time.Duration
	MaxOutputBytes      int
	LineRepeatLimit     int
	ScanLineRepeatLimit int
}

type ExecutionInput struct {
	SessionID string
	Target    string
	Turn      int
	Blocks    []PreflightResult
}

func (executor *Executor) Execute(ctx context.Context, input ExecutionInput) []ExecutionResult {
	if executor == nil {
		return nil
	}
	results := make([]ExecutionResult, len(input.Blocks))
	for index, block := range input.Blocks {
		results[index] = executor.executeBlock(ctx, input, block)
	}
	return results
}
```

Replace `internal/runtime/exec/blocks.go` with only `Language`, its two constants, and `CodeBlock`. Rewrite `blocks_test.go` as a JSON round-trip assertion for those retained fields. Delete `evidence_grade.go` and `evidence_grade_test.go`. Rewrite executor tests to retain Python/shell execution, output bounds, timeout, cancellation, repeated output, relative work directory, preflight rejection, and exact script cleanup; remove sink, Cookie, per-block JSON, and level assertions.

- [ ] **Step 7: Replace Runner and its tool set with four local tools plus external tool mounting**

Replace the public surface in `internal/runtime/loop/runner.go` with:

```go
type BlockExecutor interface {
	Execute(context.Context, exec.ExecutionInput) []exec.ExecutionResult
}

type SkillLoader func(string) (string, error)
type Sleeper func(context.Context, time.Duration) error

type RunnerConfig struct {
	MaxTurns          int
	NetworkBackoff    time.Duration
	OnEvent           func(RunnerEvent)
	SkillCatalog      []skills.Skill
	Authorizer        *authz.Authorizer
	AllowedHosts      []string
	AllowPrivateHosts bool
	MCPTools          []tool.BaseTool
}

type Runner struct {
	executor BlockExecutor
	journal  *evidence.Journal
	config   RunnerConfig
	load     SkillLoader
	sleep    Sleeper
	catalog  []skills.Skill
}

func NewRunner(executor BlockExecutor, journal *evidence.Journal, config RunnerConfig, load SkillLoader, sleep Sleeper) *Runner {
	if config.NetworkBackoff <= 0 {
		config.NetworkBackoff = 15 * time.Second
	}
	if load == nil {
		load = skills.Load
	}
	if sleep == nil {
		sleep = sleepContext
	}
	catalog := config.SkillCatalog
	if catalog == nil {
		catalog = skills.Catalog()
	}
	return &Runner{executor: executor, journal: journal, config: config, load: load, sleep: sleep, catalog: catalog}
}
```

Retain only `RunnerEvent`, `RenderExecutionResults`, `hasNetworkFriction`, `sleepContext`, `assistantSummary`, `hostOf`, and `emit`. Change `RenderExecutionResults` so it never renders evidence paths or levels. Remove the legacy `Run`, chat/history, completion-token parsing, report turns, consolidation, verifier, session pool, refusal/stuck/no-code helpers, and report-context APIs.

In `internal/runtime/loop/eino_agent.go`, define the local argument contracts:

```go
type execArgs struct {
	Command string `json:"command" jsonschema:"description=Complete Bash command to run in the engagement work directory."`
}

type executePythonArgs struct {
	Script string `json:"script" jsonschema:"description=Complete Python program to run with python3 -u in the engagement work directory."`
}

type loadSkillArgs struct {
	Name string `json:"name" jsonschema:"description=Registered PentGo skill name."`
}

type recordFindingArgs struct {
	Title          string `json:"title" jsonschema:"description=Concise unique finding title."`
	Severity       string `json:"severity" jsonschema:"description=One of critical high medium low info."`
	Description    string `json:"description" jsonschema:"description=Evidence-backed description of the observed issue."`
	EvidenceRefs   []int  `json:"evidence_refs" jsonschema:"description=Successful evidence sequence numbers supporting the finding."`
	Recommendation string `json:"recommendation" jsonschema:"description=Concrete remediation recommendation."`
}
```

Use this concrete tool-set state:

```go
type einoToolSet struct {
	mu             sync.Mutex
	executor       BlockExecutor
	journal        *evidence.Journal
	session        *sess.AgentSession
	authorizer     *authz.Authorizer
	scope          authz.Scope
	load           SkillLoader
	sleep          Sleeper
	networkBackoff time.Duration
	onEvent        func(RunnerEvent)
	action         int
	loaded         map[string]bool
	externalTools  []tool.BaseTool
}

const (
	execToolDesc = "Run a Bash command in the engagement work directory. Use it for installed commands, pipelines, redirection, and short system operations."
	executePythonToolDesc = "Run a complete Python program with python3 -u in the engagement work directory. Use it for temporary request logic, parsing, batching, and custom analysis. Print observations to stdout."
	loadSkillToolDesc = "Load one registered PentGo skill into the current engagement context."
	recordFindingToolDesc = "Record one evidence-backed finding. Every evidence_refs value must identify a successful action result. Recording does not end the engagement."
)
```

Implement local action calls through one helper:

```go
func (tools *einoToolSet) executeLanguage(ctx context.Context, toolName string, language exec.Language, source string, arguments any) (string, error) {
	startedAt := time.Now().UTC()
	tools.mu.Lock()
	tools.action++
	action := tools.action
	tools.mu.Unlock()
	block := exec.CodeBlock{Index: 1, Language: language, Code: source}
	preflight := exec.Preflight(block)
	if preflight.Approved {
		decision := tools.authorizer.Authorize(preflight.Block, tools.scope)
		if !decision.Allowed {
			preflight.Approved = false
			preflight.Rejection = decision.Reason
		}
	}
	if tools.onEvent != nil && preflight.Approved {
		tools.onEvent(RunnerEvent{Turn: action, Kind: "block_started", BlockIndex: 1, Detail: string(language)})
	}
	results := tools.executor.Execute(ctx, exec.ExecutionInput{
		SessionID: tools.session.ID,
		Target: tools.session.Target,
		Turn: action,
		Blocks: []exec.PreflightResult{preflight},
	})
	if len(results) == 0 {
		results = []exec.ExecutionResult{{Block: block, Status: exec.ExecutionFailed, ExitCode: -1, Error: "executor returned no result", StartedAt: startedAt, FinishedAt: time.Now().UTC()}}
	}
	result := results[0]
	if tools.onEvent != nil {
		tools.onEvent(RunnerEvent{Turn: action, Kind: "block_finished", BlockIndex: 1, Detail: string(result.Status)})
	}
	output := RenderExecutionResults(action, results)
	record, err := tools.journal.Record(toolName, arguments, result.Status == exec.ExecutionSucceeded, output, startedAt, result.FinishedAt)
	if err != nil {
		return "", err
	}
	if hasNetworkFriction(results) {
		if err := tools.sleep(ctx, tools.networkBackoff); err != nil {
			return "", err
		}
	}
	return record.Output, nil
}
```

Wire both fixed-language entry points exactly:

```go
func (tools *einoToolSet) exec(ctx context.Context, args execArgs) (string, error) {
	return tools.executeLanguage(ctx, "exec", exec.LanguageShell, args.Command, map[string]any{"command": args.Command})
}

func (tools *einoToolSet) executePython(ctx context.Context, args executePythonArgs) (string, error) {
	return tools.executeLanguage(ctx, "execute_python", exec.LanguagePython, args.Script, map[string]any{"script": args.Script})
}
```

Implement finding and skill control tools without Journal writes:

```go
func (tools *einoToolSet) recordFinding(_ context.Context, args recordFindingArgs) (string, error) {
	title := strings.TrimSpace(args.Title)
	severity := strings.ToLower(strings.TrimSpace(args.Severity))
	description := strings.TrimSpace(args.Description)
	recommendation := strings.TrimSpace(args.Recommendation)
	if title == "" {
		return "finding rejected: title is required", nil
	}
	if !map[string]bool{"critical": true, "high": true, "medium": true, "low": true, "info": true}[severity] {
		return "finding rejected: severity must be one of critical, high, medium, low, info", nil
	}
	if description == "" {
		return "finding rejected: description is required", nil
	}
	if recommendation == "" {
		return "finding rejected: recommendation is required", nil
	}
	if len(args.EvidenceRefs) == 0 {
		return "finding rejected: evidence_refs must contain at least one reference", nil
	}
	seenRefs := make(map[int]bool, len(args.EvidenceRefs))
	for _, ref := range args.EvidenceRefs {
		if seenRefs[ref] {
			return fmt.Sprintf("finding rejected: duplicate evidence_ref %d", ref), nil
		}
		seenRefs[ref] = true
	}
	for _, ref := range args.EvidenceRefs {
		record, ok := tools.journal.Lookup(ref)
		if !ok {
			return fmt.Sprintf("finding rejected: evidence_ref %d does not exist", ref), nil
		}
		if !record.Success {
			return fmt.Sprintf("finding rejected: evidence_ref %d is not successful", ref), nil
		}
	}
	key := strings.ToLower(title)
	tools.mu.Lock()
	defer tools.mu.Unlock()
	for _, finding := range tools.session.Findings {
		if strings.ToLower(strings.TrimSpace(finding.Title)) == key {
			return "finding rejected: title already recorded", nil
		}
	}
	tools.session.Findings = append(tools.session.Findings, sess.Finding{
		Title: title, Severity: severity, Description: description,
		EvidenceRefs: append([]int(nil), args.EvidenceRefs...), Recommendation: recommendation,
	})
	return fmt.Sprintf("finding #%d recorded", len(tools.session.Findings)), nil
}

func (tools *einoToolSet) loadSkill(_ context.Context, args loadSkillArgs) (string, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return "skill rejected: name is required", nil
	}
	tools.mu.Lock()
	defer tools.mu.Unlock()
	if tools.loaded[name] {
		return "skill " + name + " was already loaded", nil
	}
	content, err := tools.load(name)
	if err != nil {
		return "skill rejected: " + err.Error(), nil
	}
	tools.loaded[name] = true
	return "=== PENTGO SKILL CONTEXT ===\nskill: " + name + "\n" + content + "\n=== END PENTGO SKILL CONTEXT ===", nil
}
```

`record_finding` and `load_skill` never call `Journal.Record` and never exit the ADK loop.

Build and validate the complete tool list:

```go
func (tools *einoToolSet) buildTools(ctx context.Context) ([]tool.BaseTool, error) {
	execTool, err := toolutils.InferTool("exec", execToolDesc, tools.exec)
	if err != nil {
		return nil, fmt.Errorf("infer exec tool: %w", err)
	}
	pythonTool, err := toolutils.InferTool("execute_python", executePythonToolDesc, tools.executePython)
	if err != nil {
		return nil, fmt.Errorf("infer execute_python tool: %w", err)
	}
	skillTool, err := toolutils.InferTool("load_skill", loadSkillToolDesc, tools.loadSkill)
	if err != nil {
		return nil, fmt.Errorf("infer load_skill tool: %w", err)
	}
	findingTool, err := toolutils.InferTool("record_finding", recordFindingToolDesc, tools.recordFinding)
	if err != nil {
		return nil, fmt.Errorf("infer record_finding tool: %w", err)
	}
	all := []tool.BaseTool{execTool, pythonTool, skillTool, findingTool}
	seen := map[string]bool{"exec": true, "execute_python": true, "load_skill": true, "record_finding": true}
	for _, external := range tools.externalTools {
		invokable, ok := external.(tool.InvokableTool)
		if external == nil || !ok || invokable == nil {
			return nil, fmt.Errorf("external tool must implement tool.InvokableTool")
		}
		info, err := external.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("inspect external tool: %w", err)
		}
		if info == nil {
			return nil, fmt.Errorf("external tool info is nil")
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			return nil, fmt.Errorf("external tool name is empty")
		}
		if seen[name] {
			return nil, fmt.Errorf("external tool name collision: %s", name)
		}
		seen[name] = true
		all = append(all, external)
	}
	return all, nil
}
```

Construct `adk.ChatModelAgent` without `Exit` and without middleware:

```go
func newEinoAgent(ctx context.Context, chatModel model.ToolCallingChatModel, instruction string, maxTurns int, tools *einoToolSet) (*adk.ChatModelAgent, error) {
	toolList, err := tools.buildTools(ctx)
	if err != nil {
		return nil, err
	}
	if maxTurns <= 0 {
		maxTurns = 20
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "pentgo-engagement",
		Description:   "PentGo single-agent CTF engagement runtime.",
		Instruction:   instruction,
		Model:         chatModel,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList}},
		GenModelInput: literalInstructionGenModelInput,
		MaxIterations: maxTurns,
	})
}
```

The helper defaults `maxTurns` to 20 when the configured value is zero or negative.

- [ ] **Step 8: Replace the outer re-drive loop with one ADK run**

Implement `RunEino` in `internal/runtime/loop/eino_run_loop.go` as one call to `adkRunner.Run`. The service starts the session before this method. For each assistant message, increment `session.Turns` and emit a progress event. A message with tool calls continues; the first message with no tool calls is trimmed into `FinalSummary` and immediately completes or fails:

```go
func (runner *Runner) RunEino(ctx context.Context, session *sess.AgentSession, chatModel model.ToolCallingChatModel) error {
	if runner == nil || runner.executor == nil || runner.journal == nil || session == nil || chatModel == nil {
		return fmt.Errorf("eino runner dependencies are incomplete")
	}
	if session.Status != sess.SessionRunning {
		return fmt.Errorf("session must be running")
	}
	tools := &einoToolSet{
		executor: runner.executor, journal: runner.journal, session: session,
		authorizer: runner.config.Authorizer,
		scope: authz.NewScope(hostOf(session.Target), runner.config.AllowedHosts, runner.config.AllowPrivateHosts),
		load: runner.load, sleep: runner.sleep, networkBackoff: runner.config.NetworkBackoff,
		onEvent: runner.config.OnEvent, loaded: make(map[string]bool), externalTools: runner.config.MCPTools,
	}
	agentImpl, err := newEinoAgent(ctx, chatModel, buildSystemPrompt(runner.catalog), runner.config.MaxTurns, tools)
	if err != nil {
		_ = session.Fail("agent_init_error", time.Now().UTC())
		return err
	}
	adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agentImpl, EnableStreaming: false})
	iterator := adkRunner.Run(ctx, []adk.Message{schema.UserMessage("TARGET: " + session.Target + "\nTASK: " + session.Intent)})
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			if errors.Is(event.Err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				_ = session.Cancel("cancelled", time.Now().UTC())
				return nil
			}
			if errors.Is(event.Err, evidence.ErrWrite) {
				_ = session.Fail("evidence_error", time.Now().UTC())
				return event.Err
			}
			if errors.Is(event.Err, adk.ErrExceedMaxIterations) {
				_ = session.Fail("max_iterations", time.Now().UTC())
				return nil
			}
			_ = session.Fail("provider_error", time.Now().UTC())
			return event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil || event.Output.MessageOutput.Role != schema.Assistant {
			continue
		}
		message, messageErr := event.Output.MessageOutput.GetMessage()
		if messageErr != nil || message == nil {
			continue
		}
		session.Turns++
		runner.emit(RunnerEvent{Turn: session.Turns, Kind: "assistant", Detail: assistantSummary(message.Content)})
		if len(message.ToolCalls) != 0 {
			continue
		}
		session.FinalSummary = strings.TrimSpace(message.Content)
		if session.FinalSummary == "" {
			_ = session.Fail("empty_response", time.Now().UTC())
		} else {
			_ = session.Complete("agent_finished", time.Now().UTC())
		}
		return nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		_ = session.Cancel("cancelled", time.Now().UTC())
		return nil
	}
	_ = session.Fail("max_iterations", time.Now().UTC())
	return nil
}
```

There is no outer conversation slice, nudge, `Action.Exit`, recovery counter, completion token, or post-run verifier call.

- [ ] **Step 9: Replace the system prompt with the final CAI-style contract**

Keep the existing synthetic CTF/scope language and Skill catalog rendering, but replace the execution/completion section in `internal/runtime/loop/prompt.go` with these exact rules:

```text
Use a discovered specialized MCP tool when it directly matches the action.
Use exec for installed commands, Bash pipelines, redirection, and short shell operations.
Use execute_python for temporary request logic, parsing, batching, and custom analysis. Print observations to stdout.
Use load_skill when a registered PentGo skill supplies domain guidance.
Every exec, execute_python, and MCP result includes [evidence_ref: N].
Call record_finding only after the referenced successful action results support the finding.
record_finding requires title, severity, description, evidence_refs, and recommendation. It records a finding and then work continues.
When the task is complete, respond with the final summary as ordinary assistant text without a tool call.
The final summary may state that no findings were recorded. An action or finding is not required before finishing.
```

Prompt tests must assert all four local tool names and `evidence_ref`, and assert absence of `execute_code`, `complete_task`, `evidence_gate`, `declare_session`, `TASK_COMPLETE`, `MISSION_COMPLETE`, login-cookie guidance, evidence levels, and verification gates.

- [ ] **Step 10: Implement compact deterministic artifacts and report rendering**

In `internal/report/artifacts.go`, create only `work/` during writer construction and add:

```go
type Artifacts struct {
	Directory     string
	EvidenceJSONL string
	SessionJSON   string
	Markdown      string
	WorkDirectory string
}

func (writer *EngagementWriter) EvidencePath() string {
	if writer == nil || writer.stagingDir == "" {
		return ""
	}
	return filepath.Join(writer.stagingDir, "evidence.jsonl")
}
```

Delete `WriteEvidence`, evidence filename validation, `PublishWithReport`, narrative handling, and model-report fallback. `Publish` must verify `evidence.jsonl` exists as a regular file, atomically write indented `session.json` and deterministic `report.md`, rename staging to final, and return all five paths including `EvidenceJSONL`.

Replace `internal/report/markdown.go` with local rendering that starts exactly:

```go
func renderMarkdown(session *sess.AgentSession) string {
	var builder strings.Builder
	builder.WriteString("# PentGo Report\n\n## Task\n\n")
	fmt.Fprintf(&builder, "- Engagement ID: `%s`\n", inline(session.ID))
	fmt.Fprintf(&builder, "- Target: `%s`\n", inline(session.Target))
	fmt.Fprintf(&builder, "- Intent: `%s`\n", inline(session.Intent))
	fmt.Fprintf(&builder, "- Status: `%s`\n", session.Status)
	fmt.Fprintf(&builder, "- Stop reason: `%s`\n", inline(session.StopReason))
	fmt.Fprintf(&builder, "- Turns: `%d`\n", session.Turns)
	fmt.Fprintf(&builder, "- Started: `%s`\n", session.StartedAt.Format(time.RFC3339))
	if session.FinishedAt != nil {
		fmt.Fprintf(&builder, "- Finished: `%s`\n", session.FinishedAt.Format(time.RFC3339))
	}
	builder.WriteString("\n## Findings\n\n")
	if len(session.Findings) == 0 {
		builder.WriteString("No findings were recorded.\n")
	} else {
		builder.WriteString("The following findings were recorded by the Agent.\n\n")
		for _, finding := range session.Findings {
			fmt.Fprintf(&builder, "### [%s] %s\n\n%s\n\n", strings.ToUpper(finding.Severity), finding.Title, finding.Description)
			refs := make([]string, len(finding.EvidenceRefs))
			for index, ref := range finding.EvidenceRefs {
				refs[index] = fmt.Sprintf("`#%d`", ref)
			}
			fmt.Fprintf(&builder, "Evidence: %s\n\nRecommendation: %s\n\n", strings.Join(refs, ", "), finding.Recommendation)
		}
	}
	builder.WriteString("\n## Agent Summary\n\n")
	if strings.TrimSpace(session.FinalSummary) == "" {
		builder.WriteString("No final summary was produced.\n")
	} else {
		builder.WriteString(strings.TrimSpace(session.FinalSummary) + "\n")
	}
	return builder.String()
}
```

Keep `inline` for task metadata. Finding body text and final summary remain Agent-authored Markdown and are written after the deterministic headings.

- [ ] **Step 11: Remove dead configuration and text-model surfaces**

Reduce `config.AgentConfig` to:

```go
type AgentConfig struct {
	Provider                  string              `json:"provider"`
	MaxTurns                  int                 `json:"max_turns"`
	RequestTimeoutSeconds     int                 `json:"request_timeout_seconds"`
	ExecutionTimeoutSeconds   int                 `json:"execution_timeout_seconds"`
	MaxOutputBytes            int                 `json:"max_output_bytes"`
	NetworkBackoffSeconds     int                 `json:"network_backoff_seconds"`
	LineRepeatLimit           int                 `json:"line_repeat_limit"`
	ScanLineRepeatLimit       int                 `json:"scan_line_repeat_limit"`
	OpenAI                    ModelProviderConfig `json:"openai"`
	Anthropic                 ModelProviderConfig `json:"anthropic"`
	Authorization             AuthorizationConfig `json:"authorization"`
}
```

Delete defaulting/normalization for maximum findings, verification reproductions, parallel/blocks-per-turn, no-code, provider text retry, and stuck thresholds. Retain legacy configuration migration only for the still-live provider, timeout, OpenAI, and Anthropic values.

Delete `internal/agent/openai.go`, `anthropic.go`, and `client_test.go`. Reduce `internal/agent/types.go` to `ProviderConfig`, `defaultHTTPClient`, `defaultEnvLookup`, `apiKey`, `providerEndpoint`, and `requestContext`. Update `eino_model.go` comments so both providers are described as the sole runtime path. Add `eino_model_test.go` with API-key precedence, missing environment key, base URL trimming, nil context, and both Eino provider constructor tests using local configuration only.

- [ ] **Step 12: Rewrite the engagement service around one Eino run and unconditional terminal publication**

Reduce `Dependencies` to:

```go
type Dependencies struct {
	Clock           func() time.Time
	NewEngagementID func(time.Time) (string, error)
	NewEinoModel    func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error)
}
```

The non-MCP service flow in this task is exactly:

1. Validate target/intent and create engagement ID/session/writer.
2. Create `evidence.NewJournal(writer.EvidencePath())`, which guarantees an empty file.
3. Start the session.
4. Create the concrete executor without `Evidence`, `MaxParallel`, or session environment fields.
5. Build the Eino model for either configured provider.
6. Run one `Runner.RunEino`.
7. Close the Journal, call `CleanupGeneratedScripts`, and publish even when `Result.RunError` is set.

Use this finalization helper so script cleanup failure blocks publication while completed run records still publish after Agent/provider/cancellation errors:

```go
func (service *Service) finishEngagement(result Result, writer *report.EngagementWriter, journal *evidence.Journal, executor *exec.Executor, progress func(Event)) (Result, error) {
	if err := journal.Close(); err != nil && result.RunError == nil {
		result.RunError = err
		if result.Session.Status == sess.SessionRunning {
			_ = result.Session.Fail("evidence_error", service.now())
		}
	}
	if err := executor.CleanupGeneratedScripts(); err != nil {
		return result, fmt.Errorf("cleanup Runtime scripts: %w", err)
	}
	artifacts, err := writer.Publish(result.Session)
	if err != nil {
		return result, fmt.Errorf("publish engagement artifacts: %w", err)
	}
	result.Artifacts = artifacts
	progress(Event{Message: "Agent engagement finished: " + string(result.Session.Status)})
	return result, nil
}
```

When model construction fails, set `result.RunError`, fail the running session with `model_init_error`, and call the same finalizer. Delete `NewAgentClient`, `newAgentClient`, provider fallback branching, verifier/scope HTTP client, consolidation, report-model call, and report fallback events.

- [ ] **Step 13: Replace app, terminal, and command end-to-end fixtures**

Rewrite `internal/app/engagement_test.go` around an injected Eino fake and assert:

- action -> finding -> ordinary assistant text ends `done/agent_finished`;
- direct ordinary assistant text ends with an empty Journal and zero findings;
- empty response, iteration exhaustion, provider error, and cancellation publish their specified terminal state and already completed records;
- authorization rejection writes `success:false` and returns the same evidence reference to the model;
- model initialization error publishes `failed/model_init_error` with an empty Journal;
- a generated Runtime `.py`/`.sh` is absent after publish while a script-created `work/artifact.txt` remains.

Adapt terminal tests from `session.Turn` to `session.Turns`; terminal progress remains ephemeral and is absent from `session.json`.

In `cmd/pentgo/main_test.go`, have the local OpenAI-compatible `httptest` server return exactly three model responses:

```json
{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_exec_1","type":"function","function":{"name":"execute_python","arguments":"{\"script\":\"from pathlib import Path\\nPath('artifact.txt').write_text('keep')\\nprint('RESULT')\"}"}}]}}]}
{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_finding_1","type":"function","function":{"name":"record_finding","arguments":"{\"title\":\"Fixture finding\",\"severity\":\"high\",\"description\":\"Observed RESULT.\",\"evidence_refs\":[1],\"recommendation\":\"Apply fixture control.\"}"}}]}}]}
{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Completed fixture task."}}]}
```

Assert the published directory contains `evidence.jsonl`, `session.json`, `report.md`, and `work/artifact.txt`; `evidence.jsonl` has one line with `seq:1`; `session.json` has one finding and `final_summary`; the report has the finding before the summary; and no `work/turn-*.py` or `work/turn-*.sh` file exists.

- [ ] **Step 14: Delete the old runtime, verifier, session-pool, and report-model files**

Run these exact deletions after the replacement files compile conceptually:

```bash
rm internal/agent/openai.go internal/agent/anthropic.go internal/agent/client_test.go
rm internal/report/findings.go internal/report/generator.go internal/report/generator_test.go
rm internal/runtime/exec/evidence_grade.go internal/runtime/exec/evidence_grade_test.go
rm internal/runtime/loop/finding_label.go internal/runtime/loop/finding_label_test.go
rm internal/runtime/loop/history.go internal/runtime/loop/history_test.go
rm internal/runtime/loop/refusal.go internal/runtime/loop/refusal_test.go
rm internal/runtime/loop/report_context.go internal/runtime/loop/report_context_test.go
rm internal/runtime/loop/session_block.go internal/runtime/loop/session_block_test.go
rm internal/runtime/loop/session_runtime.go
rm internal/runtime/loop/validation.go internal/runtime/loop/validation_test.go
rm internal/runtime/session/pool.go internal/runtime/session/pool_test.go
rm -rf internal/runtime/verify
```

This deletion removes every framework vulnerability replay, login verifier, reproduction counter, privilege-escalation/IDOR comparison, finding DSL, confidence/verdict, and their tests. The former vulnerability-revalidation test 10 is part of this directory removal.

- [ ] **Step 15: Format and run the complete non-MCP replacement suite**

Run:

```bash
gofmt -w $(rg --files cmd internal -g '*.go')
go test ./internal/runtime/evidence ./internal/runtime/exec ./internal/runtime/session ./internal/runtime/loop ./internal/report ./internal/config ./internal/agent ./internal/app ./internal/terminal ./cmd/pentgo -count=1
go test ./... -count=1
```

Expected: PASS. `go list ./...` output contains no `pentgo/internal/runtime/verify` package.

- [ ] **Step 16: Audit removed symbols before committing**

Run:

```bash
rg 'complete_task|evidence_gate|execute_code|declare_session|TASK_COMPLETE|MISSION_COMPLETE|ConsolidateAndVerify|VerifyWithEvidence|EvidenceLevel|GradeEvidence|EvidenceSink|WriteEvidence|PublishWithReport|GenerateTerminalMarkdown|SessionPool|PENTGO_SESSION_.*_COOKIE|verification_reproductions|max_findings|max_blocks_per_turn|no_code_limit|soft_stuck_turns|hard_stuck_turns' --glob '*.go' --glob '*.md' --glob '!docs/superpowers/**'
```

Expected: no output. Historical specs/plans under `docs/superpowers/` are excluded intentionally.

- [ ] **Step 17: Commit the minimal Eino core**

```bash
git add -A
git commit -m "refactor: replace legacy runtime with natural eino flow"
```

---

### Task 4: Add the Optional Single-Server MCP Configuration and Official SDK

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: the root `agent` JSON object.
- Produces: `config.MCPConfig` and optional `AgentConfig.MCP *MCPConfig`, defaulting to nil.

- [ ] **Step 1: Write failing default and JSON loading tests**

Add to `internal/config/config_test.go`:

```go
func TestDefaultLeavesMCPDisabled(t *testing.T) {
	if Default().Agent.MCP != nil {
		t.Fatalf("default MCP = %+v", Default().Agent.MCP)
	}
}

func TestLoadPreservesSingleStdioMCPConfig(t *testing.T) {
	withConfigHome(t)
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"mcp":{"command":"/bin/fixture","args":["--stdio"],"env":{"FIXTURE_TOKEN":"TOKEN"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.MCP == nil || cfg.Agent.MCP.Command != "/bin/fixture" || len(cfg.Agent.MCP.Args) != 1 || cfg.Agent.MCP.Args[0] != "--stdio" || cfg.Agent.MCP.Env["FIXTURE_TOKEN"] != "TOKEN" {
		t.Fatalf("MCP = %+v", cfg.Agent.MCP)
	}
}
```

- [ ] **Step 2: Run config tests and verify `MCP` is missing**

Run:

```bash
go test ./internal/config -run 'TestDefaultLeavesMCPDisabled|TestLoadPreservesSingleStdioMCPConfig' -count=1
```

Expected: FAIL because `AgentConfig.MCP` and `MCPConfig` do not exist.

- [ ] **Step 3: Add the minimal configuration type**

Add to `internal/config/config.go`:

```go
type MCPConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}
```

Add this field to `AgentConfig`:

```go
	MCP *MCPConfig `json:"mcp,omitempty"`
```

Leave the default nil and preserve the exact configured command, arguments, and environment map. Startup validation belongs in `ConnectStdio`, not config normalization.

- [ ] **Step 4: Add the pinned official SDK and tidy dependencies**

Run:

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.7.0
go mod tidy
go test ./internal/config -count=1
go mod verify
```

Expected: PASS. `go.mod` contains a direct `github.com/modelcontextprotocol/go-sdk v1.7.0` requirement.

- [ ] **Step 5: Commit configuration and dependency changes**

```bash
git add internal/config/config.go internal/config/config_test.go go.mod go.sum
git commit -m "feat: configure one stdio mcp server"
```

---

### Task 5: Implement the Minimal stdio MCP-to-Eino Bridge

**Files:**
- Create: `internal/runtime/mcp/client.go`
- Create: `internal/runtime/mcp/client_test.go`

**Interfaces:**
- Consumes: `config.MCPConfig`, concrete `*evidence.Journal`, a maximum output byte count, official SDK `CommandTransport`, `ListTools`, and `CallTool`.
- Produces: `ConnectStdio(ctx context.Context, cfg config.MCPConfig, journal *evidence.Journal, maxOutputBytes int) (*Client, error)`, `(*Client).Tools() []tool.BaseTool`, and `(*Client).Close() error`.

- [ ] **Step 1: Write a local stdio fixture process and failing client tests**

Create `internal/runtime/mcp/client_test.go`. Use a package-local `TestMain`: when `PENTGO_MCP_FIXTURE=1`, run an official SDK server directly and return without calling `m.Run`; otherwise call `os.Exit(m.Run())`.

The fixture exposes one tool with the inferred object schema:

```go
type fixtureEchoInput struct {
	Value string `json:"value" jsonschema:"description=Fixture value."`
	Fail  bool   `json:"fail,omitempty" jsonschema:"description=Return an MCP tool error."`
}

type fixtureEchoOutput struct {
	Echo string `json:"echo"`
}

func TestMain(main *testing.M) {
	if os.Getenv("PENTGO_MCP_FIXTURE") == "1" {
		runFixtureServer()
		return
	}
	os.Exit(main.Run())
}

func runFixtureServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-fixture", Version: "1.0.0"}, nil)
	name := os.Getenv("PENTGO_MCP_TOOL_NAME")
	if name == "" {
		name = "fixture_echo"
	}
	sdk.AddTool(server, &sdk.Tool{Name: name, Description: "Echo a local fixture value."}, func(_ context.Context, _ *sdk.CallToolRequest, input fixtureEchoInput) (*sdk.CallToolResult, fixtureEchoOutput, error) {
		if input.Fail {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture failure"}}, IsError: true}, fixtureEchoOutput{}, nil
		}
		value := input.Value
		if value == "ENV" {
			value = os.Getenv("FIXTURE_SECRET")
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture:" + value}}}, fixtureEchoOutput{Echo: value}, nil
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		log.Print(err)
	}
}

func fixtureConfig() config.MCPConfig {
	return config.MCPConfig{Command: os.Args[0], Env: map[string]string{"PENTGO_MCP_FIXTURE": "1"}}
}

func fixtureJournal(t *testing.T, secrets ...string) *evidence.Journal {
	t.Helper()
	journal, err := evidence.NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"), secrets...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal
}

func connectFixtureClient(t *testing.T, journal *evidence.Journal) *Client {
	t.Helper()
	client, err := ConnectStdio(context.Background(), fixtureConfig(), journal, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
```

Add these tests:

```go
func TestConnectStdioDiscoversAndInvokesToolWithEvidence(t *testing.T) {
	journal := fixtureJournal(t, "TOKEN")
	client, err := ConnectStdio(context.Background(), config.MCPConfig{
		Command: os.Args[0],
		Env: map[string]string{"PENTGO_MCP_FIXTURE": "1", "FIXTURE_SECRET": "TOKEN"},
	}, journal, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tools := client.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "fixture_echo" || info.Desc != "Echo a local fixture value." {
		t.Fatalf("info = %+v", info)
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("fixture tool is not invokable")
	}
	output, err := invokable.InvokableRun(context.Background(), `{"value":"TOKEN"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "fixture:[redacted]") || !strings.Contains(output, "[evidence_ref: 1]") {
		t.Fatalf("output = %q", output)
	}
	record, ok := journal.Lookup(1)
	if !ok || !record.Success || record.Tool != "fixture_echo" || record.Output != output {
		t.Fatalf("record = %+v, %v", record, ok)
	}
}

func TestMCPIsErrorAndMalformedArgumentsAreSoftRecordedFailures(t *testing.T) {
	journal := fixtureJournal(t)
	client := connectFixtureClient(t, journal)
	invokable := client.Tools()[0].(tool.InvokableTool)
	for index, arguments := range []string{`{"value":"TARGET","fail":true}`, `[`} {
		output, err := invokable.InvokableRun(context.Background(), arguments)
		if err != nil {
			t.Fatalf("call %d error = %v", index, err)
		}
		record, ok := journal.Lookup(index + 1)
		if !ok || record.Success || record.Output != output || !strings.Contains(output, fmt.Sprintf("[evidence_ref: %d]", index+1)) {
			t.Fatalf("call %d record = %+v, %v, output = %q", index, record, ok, output)
		}
	}
}

func TestMCPOutputIsBoundedBeforeEvidenceReference(t *testing.T) {
	journal := fixtureJournal(t)
	client, err := ConnectStdio(context.Background(), fixtureConfig(), journal, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	output, err := client.Tools()[0].(tool.InvokableTool).InvokableRun(context.Background(), `{"value":"ABCDEFGHIJKLMNOPQRSTUVWXYZ"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "[output truncated]") || !strings.HasSuffix(output, "[evidence_ref: 1]") {
		t.Fatalf("output = %q", output)
	}
}
```

Also test empty command rejection, nil Journal rejection, a `toolNamePattern` table that rejects whitespace/dots/over-128-byte names, transport failure recorded as `success:false`, returned `StructuredContent` JSON, environment override delivery, `Tools` slice copy semantics, and idempotent `Close` terminating the fixture process.

- [ ] **Step 2: Run the MCP package tests and verify the bridge is missing**

Run:

```bash
go test ./internal/runtime/mcp -count=1
```

Expected: FAIL because `ConnectStdio` and `Client` do not exist.

- [ ] **Step 3: Implement connection, discovery, schema conversion, and lifecycle**

Create `internal/runtime/mcp/client.go` with these concrete types:

```go
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Client struct {
	session *sdk.ClientSession
	journal *evidence.Journal
	tools   []tool.BaseTool
	maxOutputBytes int
	closeOnce sync.Once
	closeErr error
}

type remoteTool struct {
	client *Client
	info   *schema.ToolInfo
	name   string
}

func ConnectStdio(ctx context.Context, cfg config.MCPConfig, journal *evidence.Journal, maxOutputBytes int) (*Client, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("MCP command is empty")
	}
	if journal == nil {
		return nil, fmt.Errorf("MCP evidence journal is nil")
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = 65536
	}
	command := osexec.Command(cfg.Command, cfg.Args...)
	command.Env = mergeEnv(os.Environ(), cfg.Env)
	protocolClient := sdk.NewClient(&sdk.Implementation{Name: "pentgo", Version: "1.0.0"}, nil)
	session, err := protocolClient.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect stdio MCP: %w", err)
	}
	client := &Client{session: session, journal: journal, maxOutputBytes: maxOutputBytes}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	seen := make(map[string]bool, len(listed.Tools))
	for _, definition := range listed.Tools {
		name := strings.TrimSpace(definition.Name)
		if !toolNamePattern.MatchString(name) {
			_ = session.Close()
			return nil, fmt.Errorf("invalid MCP tool name: %q", definition.Name)
		}
		if seen[name] {
			_ = session.Close()
			return nil, fmt.Errorf("duplicate MCP tool name: %s", name)
		}
		seen[name] = true
		params, err := convertSchema(definition.InputSchema)
		if err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("convert MCP tool %s schema: %w", name, err)
		}
		info := &schema.ToolInfo{Name: name, Desc: definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)}
		client.tools = append(client.tools, &remoteTool{client: client, info: info, name: name})
	}
	return client, nil
}
```

`convertSchema` marshals the SDK's `any` input schema and unmarshals it into `github.com/eino-contrib/jsonschema.Schema`. If the server returns nil, use `{"type":"object","properties":{}}`. `mergeEnv` parses the inherited environment into a map, overwrites configured keys, sorts keys, and returns stable `KEY=value` entries.

Implement both helpers exactly:

```go
func convertSchema(input any) (*einojsonschema.Schema, error) {
	if input == nil {
		input = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var converted einojsonschema.Schema
	if err := json.Unmarshal(data, &converted); err != nil {
		return nil, err
	}
	return &converted, nil
}

func mergeEnv(inherited []string, overrides map[string]string) []string {
	values := make(map[string]string, len(inherited)+len(overrides))
	for _, entry := range inherited {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		if key != "" {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
```

Return a copied tool slice and close the SDK session once:

```go
func (client *Client) Tools() []tool.BaseTool {
	if client == nil {
		return nil
	}
	return append([]tool.BaseTool(nil), client.tools...)
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	client.closeOnce.Do(func() { client.closeErr = client.session.Close() })
	return client.closeErr
}

func (tool *remoteTool) Info(context.Context) (*schema.ToolInfo, error) {
	return tool.info, nil
}
```

- [ ] **Step 4: Implement invocation as a soft action result plus durable Journal record**

Implement `remoteTool.InvokableRun` so only Journal failure is returned as a Go error:

```go
func (remote *remoteTool) InvokableRun(ctx context.Context, argumentsJSON string, _ ...tool.Option) (string, error) {
	startedAt := time.Now().UTC()
	arguments := make(map[string]any)
	if err := json.Unmarshal([]byte(argumentsJSON), &arguments); err != nil || arguments == nil {
		output := "invalid MCP tool arguments: " + argumentsJSON
		record, recordErr := remote.client.journal.Record(remote.name, map[string]any{"_raw": argumentsJSON}, false, boundText(output, remote.client.maxOutputBytes), startedAt, time.Now().UTC())
		if recordErr != nil {
			return "", recordErr
		}
		return record.Output, nil
	}
	result, err := remote.client.session.CallTool(ctx, &sdk.CallToolParams{Name: remote.name, Arguments: arguments})
	finishedAt := time.Now().UTC()
	success := err == nil && result != nil && !result.IsError
	output := ""
	if err != nil {
		output = "MCP tool call failed: " + err.Error()
	} else {
		output = renderMCPResult(result)
	}
	record, recordErr := remote.client.journal.Record(remote.name, arguments, success, boundText(output, remote.client.maxOutputBytes), startedAt, finishedAt)
	if recordErr != nil {
		return "", recordErr
	}
	return record.Output, nil
}
```

Use these result and output helpers:

```go
func renderMCPResult(result *sdk.CallToolResult) string {
	if result == nil {
		return "MCP tool returned no content"
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, content := range result.Content {
		if content == nil {
			parts = append(parts, "MCP tool returned empty content")
			continue
		}
		if textContent, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, textContent.Text)
			continue
		}
		data, err := content.MarshalJSON()
		if err != nil {
			parts = append(parts, "MCP content encoding failed: "+err.Error())
			continue
		}
		parts = append(parts, string(data))
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			parts = append(parts, "MCP structured content encoding failed: "+err.Error())
		} else {
			parts = append(parts, "structured: "+string(data))
		}
	}
	if len(parts) == 0 {
		return "MCP tool returned no content"
	}
	return strings.Join(parts, "\n")
}

func boundText(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "\n[output truncated]"
}
```

MCP `isError`, protocol/transport errors, malformed arguments, cancellation, and timeouts therefore become readable `success:false` action records; the model may correct and continue while the outer engagement context still controls termination.

- [ ] **Step 5: Format and run client tests, race checks, and dependency verification**

Run:

```bash
gofmt -w internal/runtime/mcp/client.go internal/runtime/mcp/client_test.go
go test ./internal/runtime/mcp -count=1
go test -race ./internal/runtime/mcp -count=1
go mod verify
```

Expected: PASS; all subprocesses terminate and the race detector reports no races.

- [ ] **Step 6: Commit the bridge**

```bash
git add internal/runtime/mcp/client.go internal/runtime/mcp/client_test.go
git commit -m "feat: bridge stdio mcp tools into eino"
```

---

### Task 6: Mount MCP Tools in Each Engagement and Prove the Vertical Slice

**Files:**
- Modify: `internal/app/engagement.go`
- Modify: `internal/app/engagement_test.go`
- Create: `internal/app/engagement_mcp_test.go`
- Modify: `internal/runtime/loop/eino_agent.go`
- Modify: `internal/runtime/loop/eino_run_loop_test.go`

**Interfaces:**
- Consumes: optional `AgentConfig.MCP`, `mcp.ConnectStdio`, `Client.Tools`, `Client.Close`, `RunnerConfig.MCPTools`, and Journal configured secrets.
- Produces: one MCP subprocess per configured engagement, raw discovered tool names in the Eino tool set, and close order `MCP -> Journal -> Runtime scripts -> Publish`.

- [ ] **Step 1: Write the failing full engagement test with a local stdio server**

In `internal/app/engagement_mcp_test.go`, add this package `TestMain` environment branch and official-SDK stdio fixture:

```go
type appFixtureInput struct {
	Value string `json:"value" jsonschema:"description=Fixture value."`
}

type appFixtureOutput struct {
	Echo string `json:"echo"`
}

func TestMain(main *testing.M) {
	if os.Getenv("PENTGO_APP_MCP_FIXTURE") == "1" {
		server := sdk.NewServer(&sdk.Implementation{Name: "pentgo-app-fixture", Version: "1.0.0"}, nil)
		sdk.AddTool(server, &sdk.Tool{Name: "fixture_echo", Description: "Echo a local fixture value."}, func(_ context.Context, _ *sdk.CallToolRequest, input appFixtureInput) (*sdk.CallToolResult, appFixtureOutput, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fixture:" + input.Value}}}, appFixtureOutput{Echo: input.Value}, nil
		})
		if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			log.Print(err)
		}
		return
	}
	os.Exit(main.Run())
}
```

The normal test config is:

```go
cfg := config.Default()
cfg.Agent.MCP = &config.MCPConfig{
	Command: os.Args[0],
	Env: map[string]string{
		"PENTGO_APP_MCP_FIXTURE": "1",
		"FIXTURE_SECRET": "TOKEN",
	},
}
```

Drive the injected Eino model through:

```go
turns := []*schema.Message{
	toolCallMessage("call_mcp", "fixture_echo", `{"value":"TARGET"}`),
	toolCallMessage("call_finding", "record_finding", `{"title":"MCP fixture finding","severity":"medium","description":"The fixture returned TARGET.","evidence_refs":[1],"recommendation":"Apply fixture control."}`),
	schema.AssistantMessage("MCP fixture complete.", nil),
}
```

Assert all of the following:

1. `WithTools` received `fixture_echo` with its `value` property schema alongside the four local tools.
2. The model's second request contains the MCP tool result `fixture:TARGET` and `[evidence_ref: 1]`.
3. The model's third request contains `finding #1 recorded`, proving `record_finding` continued the run.
4. `evidence.jsonl` contains one compact line with `tool:"fixture_echo"`, `success:true`, and no record for `record_finding`.
5. `session.json` contains one medium finding referencing sequence 1 and `final_summary:"MCP fixture complete."`.
6. `report.md` renders the MCP finding before the Agent summary.
7. The fixture subprocess exits when `Service.Run` finishes.

Add an initialization failure case with `Command: filepath.Join(t.TempDir(), "missing-mcp")`; assert published `failed/mcp_init_error`, empty `evidence.jsonl`, and `Result.RunError` mentioning `connect stdio MCP`.

- [ ] **Step 2: Run the E2E test and verify Service does not connect MCP yet**

Run:

```bash
go test ./internal/app -run 'TestServiceRunsMCPStdioToolEndToEnd|TestServicePublishesMCPInitializationFailure' -count=1
```

Expected: FAIL because `Service.Run` does not call `ConnectStdio` or inject discovered tools.

- [ ] **Step 3: Create the Journal with configured MCP environment values as redaction secrets**

Add this helper in `internal/app/engagement.go`:

```go
func mcpSecrets(configuration *config.MCPConfig) []string {
	if configuration == nil {
		return nil
	}
	secrets := make([]string, 0, len(configuration.Env))
	for _, value := range configuration.Env {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}
```

Construct the Journal with `evidence.NewJournal(writer.EvidencePath(), mcpSecrets(agentConfig.MCP)...)`. This ensures configured process secrets are removed from MCP output before it reaches the Agent or disk.

- [ ] **Step 4: Connect before model construction and mount the discovered tools**

After starting the session and constructing the executor:

```go
var mcpClient *runtimeMCP.Client
if agentConfig.MCP != nil {
	connectCtx, cancel := context.WithTimeout(ctx, time.Duration(agentConfig.RequestTimeoutSeconds)*time.Second)
	mcpClient, err = runtimeMCP.ConnectStdio(connectCtx, *agentConfig.MCP, journal, agentConfig.MaxOutputBytes)
	cancel()
	if err != nil {
		result.RunError = err
		_ = session.Fail("mcp_init_error", service.now())
		return service.finishEngagement(result, writer, journal, executor, nil, progress)
	}
}
```

`ConnectStdio` uses `exec.Command`, so cancelling the connection timeout after discovery does not kill the established child process. Pass discovered tools into the runner:

```go
runner := loop.NewRunner(executor, journal, loop.RunnerConfig{
	MaxTurns:          agentConfig.MaxTurns,
	NetworkBackoff:    time.Duration(agentConfig.NetworkBackoffSeconds) * time.Second,
	OnEvent:           service.progressAdapter(progress),
	Authorizer:        authorizerFromConfig(agentConfig.Authorization),
	AllowedHosts:      agentConfig.Authorization.AllowedHosts,
	AllowPrivateHosts: agentConfig.Authorization.PrivateAllowed(),
	MCPTools:          mcpClient.Tools(),
}, nil, nil)
```

When `mcpClient` is nil, pass a nil tool slice.

- [ ] **Step 5: Extend finalization with explicit resource order**

Change `finishEngagement` to accept `mcpClient *runtimeMCP.Client` and perform these operations in order:

```go
if mcpClient != nil {
	if err := mcpClient.Close(); err != nil && result.RunError == nil {
		result.RunError = fmt.Errorf("close MCP client: %w", err)
		if result.Session.Status == sess.SessionRunning {
			_ = result.Session.Fail("mcp_close_error", service.now())
		}
	}
}
if err := journal.Close(); err != nil && result.RunError == nil {
	result.RunError = err
	if result.Session.Status == sess.SessionRunning {
		_ = result.Session.Fail("evidence_error", service.now())
	}
}
if err := executor.CleanupGeneratedScripts(); err != nil {
	return result, fmt.Errorf("cleanup Runtime scripts: %w", err)
}
artifacts, err := writer.Publish(result.Session)
```

All normal, failed, cancelled, provider-error, and MCP-init-error paths call this finalizer exactly once. The writer's deferred `Abort` remains the cleanup for setup/publication failures.

- [ ] **Step 6: Run the vertical slice and full loop/app regression**

Run:

```bash
gofmt -w internal/app/engagement.go internal/app/engagement_test.go internal/app/engagement_mcp_test.go internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop_test.go
go test ./internal/runtime/loop ./internal/app -count=1
go test -race ./internal/runtime/loop ./internal/app -count=1
```

Expected: PASS. The test process makes no external network request and leaves no MCP fixture subprocess running.

- [ ] **Step 7: Commit engagement wiring**

```bash
git add internal/app/engagement.go internal/app/engagement_test.go internal/app/engagement_mcp_test.go internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop_test.go
git commit -m "feat: mount mcp tools per engagement"
```

---

### Task 7: Document the Minimal Surface and Run the Removal Audit

**Files:**
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/superpowers/plans/2026-07-29-minimal-mcp-execution-tools.md`

**Interfaces:**
- Consumes: the implemented CLI/config/tool/artifact behavior.
- Produces: one unambiguous description of the final prototype and an explicit superseded marker on the old MCP plan.

- [ ] **Step 1: Mark the old MCP plan as superseded**

Immediately below its title, add:

```markdown
> **Superseded:** Use `2026-07-29-eino-mcp-evidence-slimming.md`. The completion gate, `EvidenceSink`, session injection, verifier, and report assumptions below were removed by the approved evidence-slimming design.
```

- [ ] **Step 2: Update README with the actual tools, natural completion, artifacts, and one MCP example**

Document this exact configuration shape:

```json
{
  "agent": {
    "mcp": {
      "command": "/absolute/path/to/local-mcp-server",
      "args": ["--stdio"],
      "env": {
        "FIXTURE_TOKEN": "TOKEN"
      }
    }
  }
}
```

State that the server is started once per engagement, discovered at startup, exposed under raw MCP tool names, and closed before publication. List `exec`, `execute_python`, `load_skill`, and `record_finding`; explain that ordinary assistant text ends the run and that findings reference successful `[evidence_ref: N]` results. Show only this artifact tree:

```text
eng-*/
|-- evidence.jsonl
|-- session.json
|-- report.md
`-- work/
```

- [ ] **Step 3: Replace the architecture flow and delete legacy component descriptions**

Use this architecture flow in `docs/ARCHITECTURE.md`:

```text
terminal request
  -> engagement staging + empty evidence.jsonl
  -> optional local stdio MCP discovery
  -> one Eino ChatModelAgent
     -> exec / execute_python / MCP action -> Evidence Journal -> tool result
     -> load_skill
     -> record_finding -> compact session state
     -> ordinary assistant final response
  -> close MCP + Journal
  -> remove registered Runtime scripts
  -> deterministic session.json + report.md
  -> atomic publish
```

Remove text-client, code-fence, completion gate, report model, verifier/replay, evidence grading, session pool, login verification, and Cookie injection sections. Explicitly bound MCP to one stdio client and list the deferred management/transport features from Global Constraints.

- [ ] **Step 4: Run formatting, tests, static analysis, and module verification**

Run:

```bash
gofmt -w $(rg --files cmd internal -g '*.go')
go test ./... -count=1
go test -race ./...
go vet ./...
go build ./...
go mod tidy
go mod verify
git diff --exit-code go.mod go.sum
```

Expected: every command exits 0; `go mod tidy` leaves `go.mod` and `go.sum` unchanged.

- [ ] **Step 5: Run artifact, symbol, and network-test audits**

Run:

```bash
rg 'complete_task|evidence_gate|execute_code|declare_session|TASK_COMPLETE|MISSION_COMPLETE|ConsolidateAndVerify|VerifyWithEvidence|VerificationResult|EvidenceLevel|GradeEvidence|EvidenceSink|WriteEvidence|PublishWithReport|GenerateTerminalMarkdown|SessionPool|PENTGO_SESSION_.*_COOKIE' --glob '*.go' --glob '*.md' --glob '!docs/superpowers/**'
find internal/runtime -maxdepth 2 -type d -name verify -print
rg 'https?://' --glob '*_test.go' cmd internal
```

Expected:

- First command: no output.
- Second command: no output.
- Third command: only local `httptest` fixture values, provider configuration literals that are not contacted, and assertions about target parsing; no live smoke-test host.

Inspect one command end-to-end artifact directory and confirm:

```text
evidence.jsonl
session.json
report.md
work/artifact.txt
```

Confirm there is no `evidence/` directory, `verification-*.json`, per-call JSON, Runtime-generated `turn-*.py`/`turn-*.sh`, timeline field, login session field, evidence level, confidence, or verifier verdict.

- [ ] **Step 6: Review the final diff for prototype scope**

Run:

```bash
git diff --stat HEAD~6..HEAD
git diff --check HEAD~6..HEAD
git status --short
```

Expected: `git diff --check` exits 0. The diff adds only the Journal, one stdio MCP client, focused tests, and documentation while deleting more legacy runtime/verification/report code than it adds. The worktree contains no uncommitted implementation files.

- [ ] **Step 7: Commit documentation**

```bash
git add README.md docs/ARCHITECTURE.md docs/superpowers/plans/2026-07-29-minimal-mcp-execution-tools.md
git commit -m "docs: describe minimal eino mcp runtime"
```

## Final Acceptance Checklist

- [ ] Both OpenAI and Anthropic use the same Eino native tool-call loop.
- [ ] A plain assistant response with no tools completes the engagement as `done/agent_finished`.
- [ ] `complete_task`, `evidence_gate`, completion tokens, outer re-drive, refusal/no-code/stuck recovery, and the legacy text loop are absent.
- [ ] Every completed `exec`, `execute_python`, and MCP action seen by the Agent has one compact Journal line and matching `[evidence_ref: N]`.
- [ ] Failed, rejected, timed-out, cancelled, malformed, transport-error, and MCP `isError` actions have `success:false` records.
- [ ] `load_skill` and `record_finding` never add Journal lines.
- [ ] Every finding has exactly the five approved fields and references existing successful, unique Journal sequences.
- [ ] Finding validation is mechanical and soft; no vulnerability replay, reproduction, confidence, verdict, or evidence grade remains.
- [ ] The deleted vulnerability-revalidation test 10 has no replacement.
- [ ] `report.md` is generated locally, includes every recorded finding before the Agent summary, and handles zero findings.
- [ ] `session.json` contains only compact lifecycle data, findings, and `final_summary`; no timeline, skill list, login session, or verification data remains.
- [ ] Publication removes only exact registered Runtime script paths and preserves Agent-created work files.
- [ ] One optional local stdio MCP server is discovered once, mounted with raw collision-checked names, and closed before publication.
- [ ] No database, management plane, monitoring, alternate transport, reconnect, or compatibility artifact is introduced.
- [ ] All tests use local processes or local HTTP fixtures, and the full test/race/vet/build/module checks pass.
