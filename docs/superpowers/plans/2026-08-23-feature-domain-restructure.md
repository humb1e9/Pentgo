# Feature-Domain Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace PentGo's layer-based internal layout with a Linux-only feature-domain layout, new configuration/bootstrap behavior, user-owned skills installation, and the approved minimal CLI without changing the agent workflow semantics.

**Architecture:** `internal/core` owns only shared model/message/tool contracts. `internal/project` owns project state and is divided into `session`, `context`, and `turn`; `tools` and `model` provide external capabilities; `bootstrap.Application` is the only composition root and implements the controller consumed by `terminal`. One current SQLite schema is created by bootstrap; domain-local stores share its connection and no historical migration or repository abstraction remains.

**Tech Stack:** Go 1.25, `database/sql` with `modernc.org/sqlite`, Bubble Tea, Eino OpenAI/Anthropic adapters, MCP Go SDK, Linux/POSIX process groups, Bash.

## Global Constraints

- Support Linux only; no `GOOS` branches, Windows fallback files, or build tags remain.
- Keep exactly one project database at `<cwd>/.pentgo/pentgo.db`.
- Create only the current schema; do not retain migration versions, legacy schema compatibility, or legacy config compatibility.
- If the existing database is unusable for the current schema, remove it and create a clean database.
- Do not introduce `repository.go`, port, or future-storage abstractions.
- `core` contains only shared contracts/value types and has no internal-package dependencies.
- `bootstrap` is the only package permitted to compose project, tools, model, and terminal concrete implementations.
- `model.api_key` is the sole credential source. Never read an API-key environment variable.
- User config must be `0600`; create the full template only when it is absent, and only tighten permissions for an existing file.
- Runtime skills are user-level only: `${XDG_DATA_HOME:-$HOME/.local/share}/pentgo/skills`; never scan `<cwd>/skills`.
- `install.sh` builds from this checkout, overwrites `${XDG_BIN_HOME:-$HOME/.local/bin}/pentgo`, and initializes skills only when the destination skills directory is absent.
- Preserve model/tool/context/turn behavior except for the explicitly approved CLI/config/skills changes.
- Verify every completed task with targeted tests; final gate is `gofmt`, `go test ./...`, `go vet ./...`, and `git diff --check`.

---

## Target File Map

| Path | Responsibility |
| --- | --- |
| `internal/core/{message,tool,model}.go` | Shared messages, tools, streams, and defensive copying. |
| `internal/model/{config,provider,eino,stepper,prompt}.go` | Single-provider model configuration and provider/engine adapters. |
| `internal/tools/{config,workspace,local,mcp,registry,skill_catalog,skill_tool}.go` | Generic tools, local process control, MCP, user skill catalog, and `load_skill`. |
| `internal/project/{project,facts,sqlite}.go` | Project metadata and cross-session facts. |
| `internal/project/session/{session,worker,sqlite}.go` | Session/turn state and serialized session execution. |
| `internal/project/context/{transcript,surface,assembler,compactor,meter,checkpoint,sqlite}.go` | Transcript/context state, preparation, pruning, checkpointing, and its SQL. |
| `internal/project/turn/{service,executor,events,evidence,facts_tool,interfaces,sqlite}.go` | One-turn orchestration, events, evidence, and project-state tools. |
| `internal/bootstrap/{config,loader,database,builder,application}.go` | Config lifecycle, schema creation, construction, and application facade. |
| `internal/terminal/{controller,runtime,model,commands,render}.go` | Bubble Tea presentation using a minimal controller interface. |
| `cmd/pentgo/main.go` | Shell argument parsing and process lifetime only. |
| `install.sh` | User-level build/install and first skills initialization. |

## Task 1: Establish the new core contracts

**Files:**
- Create: `internal/core/message.go`
- Create: `internal/core/tool.go`
- Create: `internal/core/model.go`
- Move/remove: `internal/agent/message.go`, `internal/agent/context.go`, `internal/agent/tool.go`, `internal/agent/turn.go`
- Modify: every production import currently using `pentgo/internal/agent`
- Test: move and update `internal/agent/*_test.go` to the relevant consumer packages or `internal/core/*_test.go`

**Interfaces:**
- Produces `core.Message`, `core.ToolCall`, `core.Tool`, `core.ToolProvider`, `core.ToolSchemaProvider`, `core.ToolCloser`, `core.ModelStepper`, `core.ModelStepInput`, `core.ModelStreamEvent`, `core.ContextActivity`, `core.CheckpointInput`, `core.CloneMessage`, and `core.CloneArguments`.
- All later tasks import `pentgo/internal/core`, never `internal/agent`.

- [ ] **Step 1: Write core contract tests before moving implementation**

```go
func TestCloneMessageCopiesNestedArguments(t *testing.T) {
    source := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Arguments: map[string]any{"items": []any{map[string]any{"id": "one"}}}}}}
    copy := CloneMessage(source)
    copy.ToolCalls[0].Arguments["items"].([]any)[0].(map[string]any)["id"] = "two"
    if source.ToolCalls[0].Arguments["items"].([]any)[0].(map[string]any)["id"] != "one" {
        t.Fatal("source was mutated")
    }
}
```

- [ ] **Step 2: Run the core tests to verify the new package does not exist**

Run: `go test ./internal/core`

Expected: FAIL because `internal/core` is not present.

- [ ] **Step 3: Move the contracts without changing behavior**

Create `internal/core` from the current agent source. Preserve role constants, tool schema behavior, stream event types, error values, and deep-copy behavior. The package header must be `package core`; imports inside the moved code must remain standard-library only.

```go
// CloneMessage returns a detached copy of a message and JSON-like arguments.
func CloneMessage(message Message) Message { /* current clone behavior */ }

// Tool is a model-callable capability that observes ctx cancellation.
type Tool interface {
    Name() string
    Description() string
    Invoke(context.Context, map[string]any) (string, error)
}
```

- [ ] **Step 4: Rewrite imports atomically and remove `internal/agent`**

Replace each `agent.` selector with `core.` and each import path with `pentgo/internal/core`; do not leave aliases named `agent`. Remove the old directory once `go list ./...` has no importer of it.

- [ ] **Step 5: Run contract and package compilation tests**

Run: `gofmt -w internal/core && go test ./internal/core && go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core internal cmd
git rm -r internal/agent
git commit -m "refactor: move shared contracts into core"
```

## Task 2: Move and simplify model and generic tool capabilities

**Files:**
- Create: `internal/model/config.go`, `internal/model/provider.go`, `internal/model/eino.go`, `internal/model/stepper.go`, `internal/model/prompt.go`
- Create: `internal/tools/config.go`, `internal/tools/workspace.go`, `internal/tools/local.go`, `internal/tools/mcp.go`, `internal/tools/registry.go`, `internal/tools/skill_catalog.go`, `internal/tools/skill_tool.go`
- Move/remove: `internal/adapters/llm/*`, `internal/adapters/mcp/*`, `internal/adapters/builtins/*`, `internal/adapters/skillfs/*`
- Test: move the corresponding adapter tests beside their new production files

**Interfaces:**
- Produces `model.Config{Provider, BaseURL, Model, APIKey string}`, `model.New(context.Context, model.Config)`, `model.NewEngine(context.Context, any, []core.Tool)`, `model.BaseSystemPrompt()`.
- Produces `tools.Config{MaxOutputBytes int; Local LocalTools; MCP MCPServers}`, `tools.NewWorkspace(root string)`, `tools.NewLocalRegistry`, `tools.ConnectAll`, `tools.Combine`, `tools.Validate`, `tools.NewSkillCatalog(path string)`, and a `core.Tool` named `load_skill`.
- Consumes `core` only; neither package imports `project`, `bootstrap`, or `terminal`.

- [ ] **Step 1: Write failing config/credential tests**

```go
func TestModelConfigRejectsBlankAPIKey(t *testing.T) {
    _, err := New(context.Background(), Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt", APIKey: ""})
    if err == nil || !strings.Contains(err.Error(), "api_key") { t.Fatalf("err = %v", err) }
}

func TestSkillCatalogReadsOnlyItsConfiguredDirectory(t *testing.T) {
    root := t.TempDir()
    mustWrite(t, filepath.Join(root, "one.md"), "# one\n")
    catalog := NewSkillCatalog(root)
    if !catalog.HasSkills() { t.Fatal("catalog did not load user skill") }
}
```

- [ ] **Step 2: Move LLM/MCP/workspace/local implementations and compile the tests**

Run: `go test ./internal/model ./internal/tools`

Expected: FAIL until imports and public names are converted to `core`.

- [ ] **Step 3: Implement flat single-provider model configuration**

```go
type Config struct {
    Provider string `json:"provider"`
    BaseURL  string `json:"base_url"`
    Model    string `json:"model"`
    APIKey   string `json:"api_key"`
}

func (c Config) Validate() error {
    if c.Provider != "openai" && c.Provider != "anthropic" { return fmt.Errorf("unsupported provider %q", c.Provider) }
    if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.APIKey) == "" { return fmt.Errorf("model base_url, model, and api_key are required") }
    return nil
}
```

Remove provider-specific nested config and all environment lookup/fallback code. Preserve both protocol adapters selected by `Provider`.

- [ ] **Step 4: Implement generic tools with no project imports**

Keep MCP, workspace, local CLI, Linux process-group cancellation, output bounding, tool collision validation, and user-skill loading. Construct the skill catalog from an explicit directory argument; do not read `cwd/skills` anywhere in `tools`.

- [ ] **Step 5: Run focused package tests**

Run: `gofmt -w internal/model internal/tools && go test ./internal/model ./internal/tools`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/model internal/tools
git rm -r internal/adapters
git commit -m "refactor: group model and generic tools by capability"
```

## Task 3: Establish project state, sessions, and a clean database

**Files:**
- Create: `internal/project/project.go`, `internal/project/facts.go`, `internal/project/sqlite.go`
- Create: `internal/project/session/session.go`, `internal/project/session/worker.go`, `internal/project/session/sqlite.go`
- Create: `internal/bootstrap/database.go`
- Move/remove: project/session/turn portions of `internal/domain/*`, `internal/adapters/storage/project_store.go`, `internal/app/session_worker.go`
- Test: `internal/project/*_test.go`, `internal/project/session/*_test.go`, `internal/bootstrap/database_test.go`

**Interfaces:**
- Produces `project.Project`, `project.Fact`, `project.Store`, `session.Session`, `session.Turn`, `session.Worker`, and per-domain SQLite Stores sharing `*sql.DB`.
- `bootstrap.OpenDatabase(path string) (*sql.DB, error)` creates only the current schema.

- [ ] **Step 1: Write database recreation tests**

```go
func TestOpenDatabaseRecreatesInvalidDatabase(t *testing.T) {
    path := filepath.Join(t.TempDir(), "pentgo.db")
    if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil { t.Fatal(err) }
    db, err := OpenDatabase(path)
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if _, err := db.Exec("SELECT 1 FROM projects LIMIT 1"); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run the focused test before implementation**

Run: `go test ./internal/bootstrap -run TestOpenDatabaseRecreatesInvalidDatabase -count=1`

Expected: FAIL because `OpenDatabase` is absent.

- [ ] **Step 3: Define the current schema in bootstrap**

`OpenDatabase` must create `<cwd>/.pentgo` when called by the application, configure foreign keys and WAL, and execute one idempotent schema string for `projects`, `project_facts`, `sessions`, `turns`, `targets`, transcript/context tables, checkpoint data, and `evidence_records`. On any open/pragma/schema failure caused by an existing invalid file, close it, remove only `pentgo.db` plus `-wal`/`-shm`, then retry once; return the retry error if it fails.

```go
func OpenDatabase(path string) (*sql.DB, error) {
    db, err := openCurrent(path)
    if err == nil { return db, nil }
    _ = os.Remove(path); _ = os.Remove(path + "-wal"); _ = os.Remove(path + "-shm")
    return openCurrent(path)
}
```

- [ ] **Step 4: Move project/session types and SQLite access by owned table**

Keep current IDs, timestamp normalization, state transition validation, defensive clones, transaction boundaries, and worker serialization. Move `Project` and cross-session facts to `project`; move `Session`, `Turn`, and `Worker` to `project/session`. `project/sqlite.go` queries `projects`/`project_facts`; `project/session/sqlite.go` queries `sessions`/`turns`/`targets` only.

- [ ] **Step 5: Run state and schema tests**

Run: `gofmt -w internal/project internal/bootstrap/database.go && go test ./internal/project/... ./internal/bootstrap -run 'Test(OpenDatabase|Session|Turn|Project|Fact)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/project internal/bootstrap/database.go
git rm internal/domain/model.go internal/domain/project_facts.go internal/adapters/storage/project_store.go internal/app/session_worker.go
git commit -m "refactor: create project session state and clean schema"
```

## Task 4: Move project context and preserve compaction behavior

**Files:**
- Create: `internal/project/context/transcript.go`, `surface.go`, `assembler.go`, `compactor.go`, `meter.go`, `checkpoint.go`, `sqlite.go`
- Move/remove: `internal/adapters/storage/transcript_store.go`, `internal/adapters/storage/context_surface.go`, `internal/app/context_*.go`
- Test: move existing transcript/context tests under `internal/project/context`

**Interfaces:**
- Produces `context.Store`, `context.Transcript`, `context.Surface`, `context.Assembler`, `context.Compactor`, `context.Meter`, and `context.CheckpointSummarizer`.
- `CheckpointSummarizer` is defined in this package and accepts/returns only context/core values; bootstrap supplies a model-backed implementation.

- [ ] **Step 1: Write a failing compaction preservation test**

```go
func TestPreviewPruneRetainsTranscriptAndReplacesOnlySurface(t *testing.T) {
    store := newTestStore(t)
    transcript := store.Transcript("session-1")
    mustAppend(t, transcript, core.Message{Role: core.RoleTool, Content: strings.Repeat("x", 4096)})
    replacements, err := NewCompactor(store, Config{ToolResultThresholdChars: 10}).PreviewPrune(context.Background(), "session-1")
    if err != nil || len(replacements) == 0 { t.Fatalf("replacements=%v err=%v", replacements, err) }
    if len(transcript.Messages()) != 1 { t.Fatal("compaction changed raw transcript") }
}
```

- [ ] **Step 2: Move files and run the focused test**

Run: `go test ./internal/project/context -run TestPreviewPruneRetainsTranscriptAndReplacesOnlySurface -count=1`

Expected: FAIL until the stores/types use the new paths.

- [ ] **Step 3: Keep context package self-contained**

Move transcript storage, context-surface persistence, assembler, meter, and compactor together. Keep raw transcript immutable under pruning, preserve context overflow retry behavior, and preserve use of `core.CloneMessage`. Define only this dependency:

```go
type CheckpointSummarizer interface {
    Summarize(context.Context, CheckpointInput) (string, error)
}
```

Do not import `model`, `tools`, `bootstrap`, or `terminal`.

- [ ] **Step 4: Run all context tests**

Run: `gofmt -w internal/project/context && go test ./internal/project/context -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/project/context
git rm internal/app/context_assembler.go internal/app/context_compactor.go internal/app/context_meter.go internal/adapters/storage/transcript_store.go internal/adapters/storage/context_surface.go
git commit -m "refactor: place conversation context in project domain"
```

## Task 5: Move turn orchestration, evidence, facts tools, and events

**Files:**
- Create: `internal/project/turn/service.go`, `executor.go`, `events.go`, `evidence.go`, `facts_tool.go`, `interfaces.go`, `sqlite.go`
- Move/remove: `internal/app/turn_service.go`, `internal/app/tools.go`, `internal/app/events.go`, `internal/adapters/storage/evidence_store.go`
- Test: move turn/evidence tests to `internal/project/turn`

**Interfaces:**
- Produces `turn.Service`, `turn.Event`, `turn.EventKind`, `turn.EvidenceStore`, and `turn.NewService`.
- `turn/interfaces.go` consumes `core` only for external dependencies:

```go
type StepperFactory func(context.Context, *session.Session) (core.ModelStepper, error)
type ExternalTools interface { Tools(context.Context) ([]core.Tool, error) }
```

- [ ] **Step 1: Write a failing evidence ownership test**

```go
func TestToolExecutionRecordsEvidenceForCurrentTurn(t *testing.T) {
    service, runtime := newTurnFixture(t, successfulTool("probe", "ok"))
    if err := service.Run(context.Background(), runtime.SessionID, "inspect"); err != nil { t.Fatal(err) }
    record, ok := runtime.Evidence.Lookup(1)
    if !ok || record.SessionID != runtime.SessionID || record.TurnID == "" { t.Fatalf("record=%#v ok=%t", record, ok) }
}
```

- [ ] **Step 2: Run it before moving orchestration**

Run: `go test ./internal/project/turn -run TestToolExecutionRecordsEvidenceForCurrentTurn -count=1`

Expected: FAIL because the package/service fixture does not exist.

- [ ] **Step 3: Move the turn loop unchanged in behavior**

Move tool-call decoding, malformed-argument rejection, retry-on-context-overflow, ordered tool result insertion, evidence persistence, fact index injection, project fact tools, and terminal event publication. Preserve max-request defaulting in the service config; do not add setters. Define project-state tools in `facts_tool.go`; keep `load_skill` external in `tools`.

- [ ] **Step 4: Keep external coupling injectable**

`turn.Service` must accept a `StepperFactory`, external `core.ToolProvider`, optional skill loader tool, and `context.Assembler` via constructor/configuration supplied by bootstrap. It must not import `model` or concrete tools/MCP packages.

- [ ] **Step 5: Run turn tests**

Run: `gofmt -w internal/project/turn && go test ./internal/project/turn -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/project/turn
git rm internal/app/turn_service.go internal/app/tools.go internal/app/events.go internal/adapters/storage/evidence_store.go
git commit -m "refactor: move turn workflow and evidence into project"
```

## Task 6: Build bootstrap config, application, and user-level skills lifecycle

**Files:**
- Create: `internal/bootstrap/config.go`, `loader.go`, `builder.go`, `application.go`
- Modify: `internal/bootstrap/database.go`
- Create: `install.sh`
- Move/remove: `internal/config/config.go`, `internal/app/coordinator.go`, `internal/app/project_runtime.go`, obsolete `internal/app/*`
- Test: `internal/bootstrap/{loader,application}_test.go`; `install_test.sh` or shell assertions in plan execution

**Interfaces:**
- Produces `bootstrap.Config{Model model.Config; Tools tools.Config; Project project.Config}`, `bootstrap.Load() (Config, error)`, `bootstrap.New(Config, cwd string) *Application`.
- `Application` supplies all methods required by `terminal.Controller` and owns opening/closing the project-scoped database/runtime.

- [ ] **Step 1: Write failing config lifecycle tests**

```go
func TestLoadCreatesFull0600TemplateWhenMissing(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    _, err := Load()
    if !errors.Is(err, ErrConfigCreated) { t.Fatalf("err=%v", err) }
    info, err := os.Stat(ConfigFile())
    if err != nil || info.Mode().Perm() != 0o600 { t.Fatalf("mode=%v err=%v", info.Mode(), err) }
    var raw map[string]any
    mustJSON(t, os.ReadFile(ConfigFile()), &raw)
    for _, key := range []string{"model", "tools", "project"} { if _, ok := raw[key]; !ok { t.Fatalf("missing %s", key) } }
}

func TestLoadTightensExistingConfigPermissions(t *testing.T) {
    writeConfig(t, `{"model":{"provider":"openai","base_url":"x","model":"m","api_key":"k"}}`, 0o644)
    if _, err := Load(); err != nil { t.Fatal(err) }
    info, _ := os.Stat(ConfigFile())
    if info.Mode().Perm() != 0o600 { t.Fatalf("mode=%#o", info.Mode().Perm()) }
}
```

- [ ] **Step 2: Implement full template and strict loader**

`ConfigFile()` uses `${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json`. A missing file is created with the complete default JSON using `0600` and `Load` returns `ErrConfigCreated`. Existing files are chmodded to `0600` before decoding but never rewritten. Reject missing/invalid required model fields. Decode only `model`, `tools`, and `project`; reject/remove all old `agent` compatibility code.

- [ ] **Step 3: Build the Application composition root**

`Application` opens `<cwd>/.pentgo/pentgo.db`, constructs project/session/context/turn stores, constructs model and generic tool implementations, injects model checkpoint summarization and turn external interfaces, owns close order, and exposes the controller operations for terminal. It replaces both old `Coordinator` and `ProjectRuntime`; no `internal/app` package survives.

- [ ] **Step 4: Implement user-level skills and installer**

`Application` obtains skills only from:

```go
func SkillsDir() string {
    if base := os.Getenv("XDG_DATA_HOME"); base != "" { return filepath.Join(base, "pentgo", "skills") }
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".local", "share", "pentgo", "skills")
}
```

Create executable `install.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bin_dir=${XDG_BIN_HOME:-"$HOME/.local/bin"}
data_dir=${XDG_DATA_HOME:-"$HOME/.local/share"}/pentgo
mkdir -p "$bin_dir"
go build -o "$bin_dir/pentgo" "$root/cmd/pentgo"
if [ ! -d "$data_dir/skills" ]; then
  mkdir -p "$data_dir"
  cp -R "$root/skills" "$data_dir/skills"
fi
```

- [ ] **Step 5: Run bootstrap tests and installer behavior checks**

Run: `gofmt -w internal/bootstrap && go test ./internal/bootstrap -count=1 && bash -n install.sh`

Expected: PASS.

Run in a temporary HOME/XDG environment twice after adding a sentinel file to the installed skills directory; assert the sentinel remains after the second run.

- [ ] **Step 6: Commit**

```bash
git add internal/bootstrap install.sh
git rm -r internal/app internal/config
git commit -m "refactor: add bootstrap application and user configuration"
```

## Task 7: Move terminal UI and implement the approved command contract

**Files:**
- Create: `internal/terminal/controller.go`, `runtime.go`, `model.go`, `commands.go`, `render.go`
- Move/remove: `internal/cli/model.go`, `internal/cli/runtime_terminal.go`
- Test: `internal/terminal/*_test.go`

**Interfaces:**
- `terminal.Controller` is consumer-defined and implemented by `bootstrap.Application`:

```go
type Controller interface {
    OpenOrCreate(context.Context) error
    OpenExisting(context.Context) error
    Close() error
    NewSession(context.Context) (*session.Session, error)
    ResumeSession(context.Context, string) (*session.Session, error)
    Sessions() []*session.Session
    DeleteSession(context.Context, string) error
    Submit(context.Context, string, string) <-chan error
    Messages(string) []core.Message
    Events(string) <-chan turn.Event
}
```

- [ ] **Step 1: Write command behavior tests**

```go
func TestNewRejectsArguments(t *testing.T) {
    model := newTestTerminal(t)
    model.handleLine("/new unexpected")
    if !model.hasActivity(activityError, "不接受参数") { t.Fatal("missing validation error") }
}

func TestSessionDeleteDefaultsToFocusedSession(t *testing.T) {
    model, controller := newTestTerminalWithSession(t, "session-a")
    model.handleLine("/session delete")
    if controller.deleted != "session-a" { t.Fatalf("deleted=%q", controller.deleted) }
}

func TestRemovedCommandsAreUnknown(t *testing.T) {
    model := newTestTerminal(t)
    for _, line := range []string{"/quit", "/exit", "/status", "/facts", "/clear", "/project new x", "/session rename x"} {
        model.handleLine(line)
        if !model.hasActivity(activityError, "未知命令") { t.Fatalf("%s accepted", line) }
    }
}
```

- [ ] **Step 2: Move rendering/runtime and change its dependency to Controller**

Replace direct `app.Coordinator`/domain/agent imports with `terminal.Controller`, `project/session`, `project/turn`, and `core`. Keep all rendering, event display, text sanitation, session selection, Bubble Tea alternate-screen behavior, and tool-detail toggling unchanged.

- [ ] **Step 3: Implement the exact shell and TUI semantics**

- `Run` calls `OpenOrCreate`, then unconditionally creates and focuses `NewSession` named `新会话`.
- `Resume` calls `OpenExisting`, prints sessions ordered by latest update, accepts blank/latest, ordinal, or full ID, then focuses the selected session.
- TUI accepts only `/new`, `/session list`, `/session ID`, `/session delete [ID]`, `/help`.
- `/new` creates `新会话`; first normal user message changes that session name to its first 24 Unicode runes with no ellipsis.
- Bind only `Ctrl+C` to `tea.Quit`; remove `Ctrl+D`, `/quit`, and `/exit`.

- [ ] **Step 4: Run terminal tests**

Run: `gofmt -w internal/terminal && go test ./internal/terminal -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/terminal
git rm -r internal/cli
git commit -m "refactor: simplify terminal commands"
```

## Task 8: Replace the process entrypoint and remove old layout

**Files:**
- Create: `cmd/pentgo/main.go`
- Remove: `cmd/main.go`, any remaining `internal/{app,agent,adapters,cli,config,domain}` files
- Modify: `README.md` only if it documents removed shell/config/skills behavior
- Test: `cmd/pentgo/main_test.go`

**Interfaces:**
- Produces command parser accepting only `pentgo` and `pentgo resume`.
- Consumes `bootstrap.Load`, `bootstrap.ErrConfigCreated`, `bootstrap.New`, and `terminal.NewRuntime`.

- [ ] **Step 1: Write entrypoint tests**

```go
func TestParseCommandAcceptsOnlyDefaultAndResume(t *testing.T) {
    for _, args := range [][]string{nil, {"resume"}} { if _, err := parseCommand(args); err != nil { t.Fatal(err) } }
    if _, err := parseCommand([]string{"anything"}); err == nil { t.Fatal("invalid command accepted") }
}
```

- [ ] **Step 2: Implement the minimal main package**

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    os.Exit(runCommand(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
```

`runCommand` must print the generated config path/instructions and exit non-zero for `bootstrap.ErrConfigCreated`; it must not silently continue with defaults. It converts cancellation to exit code 130 and rejects all arguments except `resume` with exit code 2.

- [ ] **Step 3: Remove obsolete source directories and search for forbidden paths**

Run:

```bash
find internal -maxdepth 1 -type d | sort
grep -R 'pentgo/internal/\(app\|agent\|adapters\|cli\|config\|domain\)' -n --include='*.go' .
```

Expected: only `bootstrap`, `core`, `model`, `project`, `terminal`, and `tools` under `internal`; no matches from the grep command.

- [ ] **Step 4: Run the whole verification gate**

Run:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 5: Commit**

```bash
git add cmd README.md internal
git rm -r cmd/main.go internal/app internal/agent internal/adapters internal/cli internal/config internal/domain
git commit -m "refactor: finish feature-domain package layout"
```

## Task 9: End-to-end Linux smoke checks

**Files:**
- Modify only when a smoke test exposes a defect.
- Test: use temporary `HOME`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_BIN_HOME`, and an isolated project directory.

**Interfaces:**
- Verifies the complete binary and installer contract after all source moves.

- [ ] **Step 1: Verify first-run configuration flow**

Run the installed binary with empty temporary XDG directories. Assert it creates `${XDG_CONFIG_HOME}/pentgo/config.json`, the mode is `600`, it contains top-level `model`, `tools`, and `project`, and the program explains that model fields must be completed.

- [ ] **Step 2: Verify installer ownership behavior**

Run `install.sh`, create `${XDG_DATA_HOME}/pentgo/skills/user.md`, run `install.sh` again, and assert `user.md` remains byte-for-byte unchanged. Remove the skills directory, run it again, and assert repository built-in skills are restored.

- [ ] **Step 3: Verify terminal command contract with a test controller**

Use the terminal package tests to assert default start creates `新会话`, the first submitted message renames it to `[]rune(message)[:min(24, len([]rune(message)))]`, resume lists choices in descending `UpdatedAt`, and each deleted command emits an unknown-command activity.

- [ ] **Step 4: Run final static checks**

Run:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
git diff --check
grep -R 'GOOS\|windows\|darwin\|//go:build' -n --include='*.go' cmd internal || true
```

Expected: tests/checks pass and the final grep produces no matches.

- [ ] **Step 5: Commit final fixes, if any**

```bash
git add -A
git commit -m "test: verify restructured runtime" || true
```

## Plan Self-Review

- **Spec coverage:** Tasks 1–5 establish the approved core/project/model/tools boundaries, shared SQLite stores, context checkpoint interface, evidence ownership, and turn injection. Task 6 covers the user configuration, clean database recreation, user-level skills, and installer. Tasks 7–8 implement all approved CLI semantics and remove legacy directories. Task 9 covers first-run, installer, Linux-only, and full verification behavior.
- **Placeholder scan:** Every task names exact paths, APIs, test commands, expected outcomes, and commits. No task defers an unspecified implementation.
- **Type consistency:** `core` is established before its consumers; `project/session` and `project/turn` are named consistently; `bootstrap.Application` implements the consumer-defined `terminal.Controller`; `model.Config`, `tools.Config`, and `project.Config` form the top-level bootstrap config.
