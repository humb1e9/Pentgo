# Bootstrap Runtime Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce `internal/bootstrap` to configuration and composition by moving project lifecycle and session tool composition into `internal/project/runtime`, without retaining the legacy `bootstrap/settings` configuration path.

**Architecture:** `bootstrap.Application` builds a validated runtime configuration and constructs `runtime.Manager`. `runtime.Manager` owns workspace/project/session lifecycle plus skill/local/MCP/fact tool composition; it exposes the controller surface already consumed by `terminal`. `project/runtime` consumes `model.Config`, `tools.Config`, `project.Config`, `context.Config`, and `turn.Runtime` directly, while bootstrap remains the only cross-domain composition root.

**Tech Stack:** Go 1.25, database/sql + modernc SQLite, Bubble Tea, Eino OpenAI/Anthropic, MCP SDK.

## Global Constraints

- Linux only; do not add `GOOS`, Windows, Darwin, or build-tag branches.
- Preserve one database at `<cwd>/.pentgo/pentgo.db`; no schema migration or legacy DB compatibility.
- API keys come only from `model.api_key`; never fall back to environment variables.
- Do not introduce repository, port, factory, or compatibility abstractions beyond the single consumer-defined turn/runtime contracts already required by the package boundary.
- `bootstrap` may create dependencies, but must not own project/session lifecycle or per-session tool composition.
- Verify every task with `gofmt`, focused `go test`, and conclude with `go test ./...`, `go vet ./...`, and `git diff --check`.

---

### Task 1: Create runtime-native configuration and manager construction

**Files:**
- Create: `internal/project/runtime/config.go`
- Create: `internal/project/runtime/manager.go`
- Create: `internal/project/runtime/manager_test.go`
- Modify: `internal/bootstrap/application.go`
- Modify: `internal/terminal/controller.go`

**Interfaces:**
- Consumes: `model.Config`, `tools.Config`, `project.Config`, `context.Config`, `fs.FS`.
- Produces:
  ```go
  type runtime.Config struct {
      Model model.Config
      Tools tools.Config
      Project project.Config
  }
  type runtime.Dependencies struct {
      Clock func() time.Time
      SkillsFS fs.FS
      NewModel func(context.Context, model.Config) (einomodel.ToolCallingChatModel, error)
  }
  func runtime.NewManager(Config, string, Dependencies) *Manager
  ```
- `Manager` implements every method in `terminal.Controller` with unchanged user-visible behavior.

- [ ] **Step 1: Write the failing manager construction test**

```go
func TestNewManagerUsesDomainConfig(t *testing.T) {
    manager := NewManager(Config{
        Model: model.Config{Provider: "openai", BaseURL: "https://example.test/v1", Model: "fixture", APIKey: "key"},
        Tools: tools.DefaultConfig(),
        Project: project.DefaultConfig(),
    }, t.TempDir(), Dependencies{SkillsFS: os.DirFS(t.TempDir())})
    if manager == nil { t.Fatal("nil manager") }
}
```

- [ ] **Step 2: Run the failing test**

Run: `go test ./internal/project/runtime -run TestNewManagerUsesDomainConfig -count=1`

Expected: FAIL because `NewManager` and `runtime.Config` do not exist.

- [ ] **Step 3: Add the minimal runtime config and Manager constructor**

```go
type Config struct { Model model.Config; Tools tools.Config; Project project.Config }
type Dependencies struct { Clock func() time.Time; SkillsFS fs.FS; NewModel func(context.Context, model.Config) (einomodel.ToolCallingChatModel, error) }
```

Move the existing `Coordinator` fields/methods into `runtime.Manager`, rename `Coordinator` to `Manager`, and replace legacy `config.AgentConfig` reads with the corresponding domain config field. Keep the same lock ownership and error text unless the legacy-config wording is present.

- [ ] **Step 4: Make Application compose Manager directly**

```go
type Application struct { *runtime.Manager; Config Config }
func NewApplication(cfg Config, root string, skills fs.FS) *Application {
    return &Application{Manager: runtime.NewManager(runtime.Config{Model: cfg.Model, Tools: cfg.Tools, Project: cfg.Project}, root, runtime.Dependencies{SkillsFS: skills, NewModel: model.New}), Config: cfg}
}
```

Update `terminal.Controller` imports/signatures from bootstrap event types to the session event type or a final runtime-exported alias so terminal no longer requires bootstrap types.

- [ ] **Step 5: Run focused tests**

Run: `gofmt -w internal/project/runtime internal/bootstrap/application.go internal/terminal/controller.go && go test ./internal/project/runtime ./internal/bootstrap ./internal/terminal`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/project/runtime internal/bootstrap/application.go internal/terminal/controller.go
git commit -m "refactor: move project manager into runtime"
```

### Task 2: Move project runtime and session tool composition

**Files:**
- Create: `internal/project/runtime/runtime.go`
- Create: `internal/project/runtime/tools.go`
- Create: `internal/project/runtime/skills.go`
- Create: `internal/project/runtime/runtime_test.go`
- Move: `internal/bootstrap/project_runtime.go` → `internal/project/runtime/runtime.go`
- Move: `internal/bootstrap/tool_provider.go` → `internal/project/runtime/tools.go`
- Move: `internal/bootstrap/tools.go` → `internal/project/runtime/tools.go` (merge by responsibility)
- Move: `internal/bootstrap/skill_catalog.go` → `internal/project/runtime/skills.go`
- Modify: `internal/project/turn/runtime.go`
- Modify: `internal/project/turn/service.go`

**Interfaces:**
- Consumes: `project.ProjectStore`, `session.Worker`, `turn.Runtime`, `tools.Registry`, `tools.LocalRegistry`, `tools.Clients`.
- Produces a `*runtime.ProjectRuntime` that satisfies `turn.Runtime` and a per-session tool builder passed to `turn.TurnServiceConfig.BuildTools`.

- [ ] **Step 1: Write the failing tool-composition test**

```go
func TestBuildSessionToolsRejectsSkillNameCollision(t *testing.T) {
    runtime := newRuntimeFixture(t, toolNamed("load_skill"))
    _, err := runtime.BuildSessionTools(context.Background(), runtime.session, nil)
    if err == nil || !strings.Contains(err.Error(), "tool name collision: load_skill") {
        t.Fatalf("err=%v", err)
    }
}
```

- [ ] **Step 2: Run the failing test**

Run: `go test ./internal/project/runtime -run TestBuildSessionToolsRejectsSkillNameCollision -count=1`

Expected: FAIL because `BuildSessionTools` is not defined in the runtime package.

- [ ] **Step 3: Move implementation without changing behavior**

Move `ProjectRuntime`, `runtimeToolProvider`, `combinedToolProvider`, skill scan state, and tool collision validation to `project/runtime`. Replace bootstrap package references in the moved files with direct calls to `tools`, `project`, `session`, `context`, and `turn` packages. Keep `NewSkillTool` in `tools`; call it from `BuildSessionTools` only when the scanned catalog is nonempty.

Use the existing turn configuration hook:

```go
BuildTools: func(ctx context.Context, state turn.Runtime, session *session.Session, external []core.Tool) ([]core.Tool, error) {
    return projectRuntime.BuildSessionTools(ctx, session, external)
},
```

- [ ] **Step 4: Update turn runtime wiring**

Ensure `*runtime.ProjectRuntime` satisfies:

```go
type Runtime interface {
    Transcript(string) *project.TranscriptStore
    FactSnapshot(context.Context) (string, error)
    Tools(context.Context) ([]core.Tool, error)
    Evidence() *EvidenceStore
    Persist(*session.Session) error
    Publish(string)
    Emit(string, Event)
    ContextPreparer() context.ContextPreparer
}
```

Do not add another adapter layer; methods live directly on `ProjectRuntime`.

- [ ] **Step 5: Run focused tests**

Run: `gofmt -w internal/project/runtime internal/project/turn && go test ./internal/project/runtime ./internal/project/turn`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/project/runtime internal/project/turn
git commit -m "refactor: move project runtime tool composition"
```

### Task 3: Remove bootstrap compatibility layer and legacy settings

**Files:**
- Delete: `internal/bootstrap/coordinator.go`
- Delete: `internal/bootstrap/project_runtime.go`
- Delete: `internal/bootstrap/tools.go`
- Delete: `internal/bootstrap/tool_provider.go`
- Delete: `internal/bootstrap/skill_catalog.go`
- Delete: `internal/bootstrap/turn_runtime.go`
- Delete: `internal/bootstrap/turn_context.go`
- Delete: `internal/bootstrap/turn_service_facade.go`
- Delete: `internal/bootstrap/context_{assembler,compactor}.go`
- Delete: `internal/bootstrap/project_fact_{ledger,index}.go`
- Delete: `internal/bootstrap/events.go`
- Delete: `internal/bootstrap/settings/config.go`
- Delete: associated compatibility-only tests
- Modify: `internal/bootstrap/context_checkpoint_summarizer.go`
- Modify: `internal/bootstrap/application.go`
- Modify: `internal/bootstrap/config.go`
- Modify: `internal/bootstrap/loader.go`
- Modify: `cmd/pentgo/main.go`
- Modify: `internal/terminal/{controller.go,model.go,runtime.go}`

**Interfaces:**
- Consumes: `runtime.Manager` from Task 1 and `runtime.ProjectRuntime` from Task 2.
- Produces: no import/reference under `internal/bootstrap/settings` and no compatibility aliases in bootstrap.

- [ ] **Step 1: Write the failing static path test**

```go
func TestNoLegacyBootstrapSettingsImport(t *testing.T) {
    output, err := exec.Command("git", "grep", "internal/bootstrap/settings", "--", "*.go").CombinedOutput()
    if err == nil || len(output) != 0 { t.Fatalf("legacy imports: %s", output) }
}
```

Place it temporarily in `internal/bootstrap/application_test.go`; remove it only if the repository already has an equivalent static validation script.

- [ ] **Step 2: Replace legacy config conversion with direct values**

`context_checkpoint_summarizer.go` must receive a `model.Config` and a model constructor rather than `settings.AgentConfig`. Model checkpoint provider/model overrides are removed unless they are represented in the approved new config; the sole model configuration is `model.Config`.

```go
type CheckpointModelFactory func(context.Context, model.Config) (einomodel.ToolCallingChatModel, error)
func NewModelCheckpointSummarizer(factory CheckpointModelFactory, cfg model.Config) context.CheckpointSummarizer
```

Use `model.New` through the injected factory. Do not recreate OpenAI/Anthropic provider blocks.

- [ ] **Step 3: Delete facades and settings**

Update all imports to `project/runtime`, `project/context`, `project/turn`, `project/session`, `project`, `tools`, or `model`. Delete the listed bootstrap facade files and `bootstrap/settings`, then remove now-empty directories.

- [ ] **Step 4: Verify absence and behavior**

Run:

```bash
grep -R 'internal/bootstrap/settings\|internal/(app|agent|adapters|cli|config|domain)' --include='*.go' cmd internal && exit 1 || true
grep -R 'GOOS\|windows\|darwin\|//go:build' --include='*.go' cmd internal && exit 1 || true
gofmt -w cmd internal
go test ./...
go vet ./...
git diff --check
```

Expected: no grep matches; all Go checks PASS.

- [ ] **Step 5: Verify installed first-run contract**

```bash
tmp=$(mktemp -d)
XDG_CONFIG_HOME="$tmp/config" XDG_BIN_HOME="$tmp/bin" XDG_DATA_HOME="$tmp/data" ./install.sh
set +e
(cd "$tmp" && XDG_CONFIG_HOME="$tmp/config" XDG_DATA_HOME="$tmp/data" "$tmp/bin/pentgo" >/dev/null 2>"$tmp/err")
status=$?
set -e
test "$status" -eq 1
test "$(stat -c %a "$tmp/config/pentgo/config.json")" = 600
grep -q '"model"' "$tmp/config/pentgo/config.json"
grep -q '"tools"' "$tmp/config/pentgo/config.json"
grep -q '"project"' "$tmp/config/pentgo/config.json"
grep -q 'set model.model and model.api_key' "$tmp/err"
rm -rf "$tmp"
```

Expected: all commands succeed.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: reduce bootstrap to composition root"
```

## Self-Review

- **Spec coverage:** Task 1 moves and renames the lifecycle facade; Task 2 moves project runtime and tool composition; Task 3 deletes the settings/facade layer and verifies Linux/config/install contracts.
- **No placeholders:** Each task names exact files, interfaces, commands, and the behavior to preserve.
- **Type consistency:** `runtime.Manager` uses domain config; `runtime.ProjectRuntime` directly implements `turn.Runtime`; `terminal.Controller` consumes the Manager surface; bootstrap only creates these values.
