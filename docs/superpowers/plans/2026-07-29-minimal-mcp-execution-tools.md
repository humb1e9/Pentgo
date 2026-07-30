# Minimal MCP and Split Execution Tools Implementation Plan

> **Superseded:** Use `2026-07-29-eino-mcp-evidence-slimming.md`. The completion gate, `EvidenceSink`, session injection, verifier, and report assumptions below were removed by the approved evidence-slimming design.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the public `execute_code(language, code)` tool with focused `exec(command)` and `execute_python(script)` tools, then add one local stdio MCP server whose discovered tools participate in the same Eino single-agent loop.

**Architecture:** Keep PentGo's existing `Preflight -> Authorizer -> Executor` path as the only local process boundary. `exec` and `execute_python` are thin Eino adapters over that path. A small MCP client uses the official Go SDK to start one configured stdio subprocess, converts `tools/list` JSON Schemas into Eino tools, forwards `tools/call`, and exposes successful MCP calls to the existing completion gate. There is no internal MCP server or management layer.

**Tech Stack:** Go 1.25, CloudWeGo Eino/ADK v0.9.13, `github.com/modelcontextprotocol/go-sdk` v1.7.0, standard library `os/exec`, existing PentGo executor and tests.

## Global Constraints

- Support exactly one local stdio MCP server per engagement.
- Keep `exec` fixed to Bash and `execute_python` fixed to `python3 -u`; both run in the engagement `work/` directory through the existing executor.
- Keep existing preflight, target scope, timeout, cancellation, output limits, session environment injection, redaction, report, and verifier behavior.
- Remove `execute_code` from the Eino tool surface; do not retain a compatibility alias.
- Prefer a discovered specialized MCP tool; use `exec` for installed commands and shell composition; use `execute_python` for temporary scripting, parsing, batching, and custom requests.
- MCP tool failures return readable tool results so the model can correct its next action; only a successful MCP result satisfies the existing completion gate.
- Do not add MCP execution IDs, monitoring storage, RBAC, HITL, tool statistics, hot reload, reconnect, circuit breakers, multiple servers, HTTP/SSE transports, resources, prompts, sampling, or multimodal results.
- Do not add `mcp-call-*.json`, event logs, new evidence levels, or any other evidence/report format in this implementation. Evidence simplification remains a separate design task.
- All network-facing tests use local fixtures only. MCP integration tests use a local stdio subprocess and make no external request.

---

## File Map

**Create**

- `internal/runtime/mcp/client.go` - stdio MCP lifecycle, tool discovery, Eino schema adapter, invocation, and successful-call state.
- `internal/runtime/mcp/client_test.go` - local stdio fixture and client-level discovery/invocation tests.
- `internal/app/engagement_mcp_test.go` - full `Service.Run` MCP-only engagement test.

**Modify**

- `go.mod`, `go.sum` - add the official MCP Go SDK at v1.7.0.
- `internal/config/config.go` - add the optional single-server stdio configuration.
- `internal/config/config_test.go` - lock default-disabled and JSON-loading behavior.
- `internal/runtime/loop/eino_agent.go` - split local execution tools, accept external Eino tools, and extend the completion gate.
- `internal/runtime/loop/eino_run_loop.go` - treat both local execution tool names as executor results.
- `internal/runtime/loop/eino_run_loop_test.go` - replace `execute_code` calls and test both new local tools.
- `internal/runtime/loop/prompt.go` - document specialized-MCP-first tool selection and the two local execution tools.
- `internal/runtime/loop/prompt_content_test.go` - lock the new prompt/tool contract.
- `internal/runtime/loop/runner.go` - carry MCP tools and successful-call callback in `RunnerConfig`.
- `internal/app/engagement.go` - connect, inject, and close the configured MCP client.
- `internal/app/engagement_test.go` - update Eino helpers and expected local tool names.
- `README.md` - document the tool split and minimal stdio configuration.
- `docs/ARCHITECTURE.md` - add the MCP client boundary and explicitly exclude a management plane.

---

### Task 1: Split the Local Execution Tool Surface

**Files:**
- Modify: `internal/runtime/loop/eino_agent.go:24-217,397-446`
- Modify: `internal/runtime/loop/eino_run_loop.go:243-269`
- Modify: `internal/runtime/loop/eino_run_loop_test.go`
- Modify: `internal/app/engagement_test.go:464-533`

**Interfaces:**
- Consumes: existing `exec.CodeBlock`, `exec.Preflight`, `BlockExecutor.Execute`, `RenderExecutionResults`, and session environment injection.
- Produces: Eino tools `exec(command string)` and `execute_python(script string)` backed by one shared `executeLanguage` method.

- [ ] **Step 1: Write failing tool-surface tests**

Add a focused test to `internal/runtime/loop/eino_run_loop_test.go` that drives both new tools and asserts their fixed languages:

```go
func TestRunEinoExecAndExecutePythonUseFixedLanguages(t *testing.T) {
	executor := &recordingExecutor{results: []exec.ExecutionResult{
		{Block: exec.CodeBlock{Index: 1, Language: exec.LanguageShell}, Status: exec.ExecutionSucceeded, Stdout: "shell\n"},
		{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "python\n"},
	}}
	fake := &scriptedToolModel{turns: []*schema.Message{
		toolCallMessage("shell", "exec", `{"command":"printf shell"}`),
		toolCallMessage("python", "execute_python", `{"script":"print('python')"}`),
		exitMessage("done"),
	}}
	runner := NewRunner(nil, executor, einoTestConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatal(err)
	}
	if len(executor.inputs) != 2 {
		t.Fatalf("execution count = %d, want 2", len(executor.inputs))
	}
	if got := executor.inputs[0].Blocks[0].Block.Language; got != exec.LanguageShell {
		t.Fatalf("exec language = %q, want shell", got)
	}
	if got := executor.inputs[1].Blocks[0].Block.Language; got != exec.LanguagePython {
		t.Fatalf("execute_python language = %q, want python", got)
	}
}
```

Change the existing main-path fixture to call `execute_python`:

```go
func einoPython(pythonCode string) *schema.Message {
	args, _ := json.Marshal(map[string]string{"script": pythonCode})
	return einoToolCall("execute_python", string(args))
}
```

Replace `einoExec(...)` call sites with `einoPython(...)`. Replace test tool calls shaped as `{"language":"python","code":"..."}` with `{"script":"..."}` and the tool name `execute_python`.

- [ ] **Step 2: Run the focused tests and verify the new tools are absent**

Run:

```bash
go test ./internal/runtime/loop ./internal/app -run 'TestRunEinoExecAndExecutePythonUseFixedLanguages|TestServiceUsesFifthModelRequestForFinalReportAfterEmptyConsolidationRetry' -count=1
```

Expected: FAIL because `exec` and `execute_python` are not registered and the current tool is named `execute_code`.

- [ ] **Step 3: Replace the public argument type and extract one shared executor method**

In `internal/runtime/loop/eino_agent.go`, replace `executeCodeArgs` and `executeCode` with:

```go
type execArgs struct {
	Command string `json:"command" jsonschema:"description=Complete Bash command to run in the engagement work directory."`
}

type executePythonArgs struct {
	Script string `json:"script" jsonschema:"description=Complete Python program to run with python3 -u in the engagement work directory. Print observations to stdout."`
}

func (ts *einoToolSet) exec(ctx context.Context, args execArgs) (string, error) {
	return ts.executeLanguage(ctx, exec.LanguageShell, args.Command)
}

func (ts *einoToolSet) executePython(ctx context.Context, args executePythonArgs) (string, error) {
	return ts.executeLanguage(ctx, exec.LanguagePython, args.Script)
}

func (ts *einoToolSet) executeLanguage(ctx context.Context, language exec.Language, code string) (string, error) {
	ts.mu.Lock()
	ts.turn++
	turn := ts.turn
	ts.mu.Unlock()

	block := exec.CodeBlock{Index: 1, Language: language, Code: code}
	preflight := exec.Preflight(block)
	authzBlocked := false
	if preflight.Approved {
		if decision := ts.authorizer.Authorize(preflight.Block, ts.scope); !decision.Allowed {
			preflight.Approved = false
			preflight.Rejection = decision.Reason
			authzBlocked = true
		}
	}
	approved := preflight.Approved

	if ts.onBlockEvent != nil && approved {
		ts.onBlockEvent(RunnerEvent{Turn: turn, Kind: "block_started", BlockIndex: block.Index, Detail: string(language)})
	}
	extraEnv := ts.sessionEnv()
	results := ts.executor.Execute(ctx, exec.ExecutionInput{
		SessionID: ts.sessionID,
		Target:    ts.target,
		Turn:      turn,
		Blocks:    []exec.PreflightResult{preflight},
		ExtraEnv:  extraEnv,
	})
	safeResults := redactExecutionResults(results, extraEnv)
	if ts.onBlockEvent != nil {
		for _, result := range safeResults {
			ts.onBlockEvent(RunnerEvent{Turn: turn, Kind: "block_finished", BlockIndex: result.Block.Index, Detail: string(result.Status)})
		}
	}

	ts.mu.Lock()
	ts.resultStash = append(ts.resultStash, einoExecOutcome{results: safeResults, authzBlocked: authzBlocked})
	if approved {
		ts.hasEvidence = true
	}
	ts.mu.Unlock()

	if !approved {
		return renderPreflightRejections(turn, []exec.PreflightResult{preflight}), nil
	}
	rendered := RenderExecutionResults(turn, safeResults)
	if hasNetworkFriction(results) {
		if err := ts.sleep(ctx, ts.networkBackoff); err != nil {
			return rendered, err
		}
		rendered += "\nNETWORK FRICTION: execution output indicates throttling or transport failure. Adjust rate, target, or strategy before continuing."
	}
	return rendered, nil
}
```

Delete `supportedToolLanguage`; the model no longer chooses the interpreter.

- [ ] **Step 4: Register the two tools and consume both result types**

Replace the `execute_code` registration in `buildTools`:

```go
execTool, err := toolutils.InferTool("exec", execToolDesc, ts.exec)
if err != nil {
	return nil, fmt.Errorf("infer exec tool: %w", err)
}
pythonTool, err := toolutils.InferTool("execute_python", executePythonToolDesc, ts.executePython)
if err != nil {
	return nil, fmt.Errorf("infer execute_python tool: %w", err)
}
```

Return both tools before the control tools:

```go
return []tool.BaseTool{execTool, pythonTool, skillTool, sessionTool, gateTool}, nil
```

Define concise descriptions:

```go
const (
	execToolDesc = "Run a Bash command in the engagement work directory. Use it for installed commands, pipelines, redirection, and short system operations."
	executePythonToolDesc = "Run a complete Python program with python3 -u in the engagement work directory. Use it for temporary request logic, parsing, batching, and custom analysis. Print observations to stdout."
)
```

In `consumeEinoToolResult`, route both names through the existing executor-result branch:

```go
case "exec", "execute_python":
	if outcome, ok := tools.popResults(); ok {
		if outcome.authzBlocked {
			session.AddEvent(session.Turn, "recovery", "authorization_blocked", time.Now().UTC())
		}
		runner.recordReportBlocks(outcome.results)
		session.AddEvent(session.Turn, "execution", fmt.Sprintf("%d block(s)", len(outcome.results)), time.Now().UTC())
	}
	runner.history.Append("user", strings.TrimSpace(message.Content))
```

- [ ] **Step 5: Run the local execution regression tests**

Run:

```bash
go test ./internal/runtime/loop ./internal/app -count=1
```

Expected: PASS. Existing session redaction, scope blocking, stuck detection, report accumulation, and completion-gate tests must pass using `exec` or `execute_python`.

- [ ] **Step 6: Commit the isolated tool split**

```bash
git add internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop.go internal/runtime/loop/eino_run_loop_test.go internal/app/engagement_test.go
git commit -m "refactor: split local execution tools"
```

---

### Task 2: Add the Minimal MCP Configuration and SDK Dependency

**Files:**
- Modify: `internal/config/config.go:13-48,69-160`
- Modify: `internal/config/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: existing root `agent` JSON configuration and `request_timeout_seconds`.
- Produces: `config.MCPConfig` and optional `AgentConfig.MCP *MCPConfig`.

- [ ] **Step 1: Write failing configuration tests**

Add:

```go
func TestDefaultLeavesMCPDisabled(t *testing.T) {
	if Default().Agent.MCP != nil {
		t.Fatalf("default MCP = %+v, want nil", Default().Agent.MCP)
	}
}

func TestLoadPreservesSingleStdioMCPConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent":{"mcp":{"command":"/bin/fixture-mcp","args":["--stdio"],"env":{"FIXTURE":"1"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.MCP == nil || cfg.Agent.MCP.Command != "/bin/fixture-mcp" {
		t.Fatalf("MCP config = %+v", cfg.Agent.MCP)
	}
	if !reflect.DeepEqual(cfg.Agent.MCP.Args, []string{"--stdio"}) || cfg.Agent.MCP.Env["FIXTURE"] != "1" {
		t.Fatalf("MCP config = %+v", cfg.Agent.MCP)
	}
}
```

- [ ] **Step 2: Run the configuration tests and verify the field is absent**

Run:

```bash
go test ./internal/config -run 'TestDefaultLeavesMCPDisabled|TestLoadPreservesSingleStdioMCPConfiguration' -count=1
```

Expected: FAIL because `AgentConfig.MCP` does not exist.

- [ ] **Step 3: Add the configuration types**

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

Do not create a default MCP value and do not normalize an absent block.

- [ ] **Step 4: Add the official MCP SDK**

Run:

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.7.0
go mod tidy
```

Expected: `github.com/modelcontextprotocol/go-sdk v1.7.0` appears as a direct requirement after production code imports it in Task 3. If `go mod tidy` temporarily removes it before Task 3, run the same two commands after creating `client.go`.

- [ ] **Step 5: Run configuration tests**

Run:

```bash
go test ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit configuration and dependency changes**

```bash
git add internal/config/config.go internal/config/config_test.go go.mod go.sum
git commit -m "feat: configure one stdio mcp server"
```

---

### Task 3: Build the Stdio MCP Client and Eino Tool Adapter

**Files:**
- Create: `internal/runtime/mcp/client.go`
- Create: `internal/runtime/mcp/client_test.go`

**Interfaces:**
- Consumes: `config.MCPConfig`, MCP `tools/list`, MCP `tools/call`, Eino `tool.BaseTool`.
- Produces:
  - `func ConnectStdio(context.Context, config.MCPConfig) (*Client, error)`
  - `func (*Client) Tools() []tool.BaseTool`
  - `func (*Client) HasSuccessfulCall() bool`
  - `func (*Client) Close() error`

- [ ] **Step 1: Write a local stdio fixture and failing client test**

Create `internal/runtime/mcp/client_test.go` with a subprocess test fixture. The fixture runs only when the environment marker is present:

```go
package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"pentgo/internal/config"

	"github.com/cloudwego/eino/components/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPFixtureProcess(t *testing.T) {
	if os.Getenv("PENTGO_MCP_FIXTURE") != "1" {
		return
	}
	type echoArgs struct {
		Value string `json:"value" jsonschema:"value to echo"`
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "pentgo-fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "fixture_echo", Description: "Echo a fixture value"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "fixture:" + args.Value}}}, nil, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestConnectStdioDiscoversAndInvokesTextTool(t *testing.T) {
	client, err := ConnectStdio(context.Background(), config.MCPConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPFixtureProcess"},
		Env:     map[string]string{"PENTGO_MCP_FIXTURE": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil || info.Name != "fixture_echo" {
		t.Fatalf("tool info = %+v, err = %v", info, err)
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool type = %T, want InvokableTool", tools[0])
	}
	result, err := invokable.InvokableRun(context.Background(), `{"value":"TARGET"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "fixture:TARGET") || !client.HasSuccessfulCall() {
		t.Fatalf("result/success = %q/%v", result, client.HasSuccessfulCall())
	}
}
```

- [ ] **Step 2: Run the client test and verify the package is absent**

Run:

```bash
go test ./internal/runtime/mcp -run TestConnectStdioDiscoversAndInvokesTextTool -count=1
```

Expected: FAIL because `ConnectStdio` and `Client` do not exist.

- [ ] **Step 3: Implement the minimal client lifecycle**

Create `internal/runtime/mcp/client.go` with these concrete fields and connection behavior:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"

	"pentgo/internal/config"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var modelToolName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type Client struct {
	session    *sdkmcp.ClientSession
	tools      []tool.BaseTool
	successful atomic.Bool
}

func ConnectStdio(ctx context.Context, cfg config.MCPConfig) (*Client, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, fmt.Errorf("mcp command is empty")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("find mcp command %q: %w", command, err)
	}
	cmd := exec.Command(path, cfg.Args...)
	cmd.Env = mergeEnv(cfg.Env)
	sdkClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "PentGo", Version: "prototype"}, nil)
	session, err := sdkClient.Connect(ctx, &sdkmcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect stdio mcp: %w", err)
	}
	c := &Client{session: session}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("list mcp tools: %w", err)
	}
	for _, remote := range listed.Tools {
		wrapped, err := c.wrapTool(remote)
		if err != nil {
			_ = session.Close()
			return nil, err
		}
		c.tools = append(c.tools, wrapped)
	}
	return c, nil
}

func mergeEnv(extra map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index > 0 {
			values[entry[:index]] = entry[index+1:]
		}
	}
	for key, value := range extra {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func (c *Client) Tools() []tool.BaseTool {
	return append([]tool.BaseTool(nil), c.tools...)
}

func (c *Client) HasSuccessfulCall() bool {
	return c != nil && c.successful.Load()
}

func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}
```

- [ ] **Step 4: Implement schema conversion and invocation**

Complete `client.go` with an Eino wrapper that supports text and structured JSON results:

```go
type bridgeTool struct {
	client *Client
	info   *schema.ToolInfo
	name   string
}

func (c *Client) wrapTool(remote *sdkmcp.Tool) (tool.BaseTool, error) {
	if remote == nil || !modelToolName.MatchString(remote.Name) {
		return nil, fmt.Errorf("invalid mcp tool name %q", remote.Name)
	}
	raw, err := json.Marshal(remote.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp tool %q schema: %w", remote.Name, err)
	}
	var input jsonschema.Schema
	if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("decode mcp tool %q schema: %w", remote.Name, err)
		}
	}
	if input.Type == "" {
		input.Type = string(schema.Object)
	}
	return &bridgeTool{
		client: c,
		name:   remote.Name,
		info: &schema.ToolInfo{
			Name:        remote.Name,
			Desc:        remote.Description,
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&input),
		},
	}, nil
}

func (t *bridgeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *bridgeTool) InvokableRun(ctx context.Context, argumentsJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if strings.TrimSpace(argumentsJSON) != "" && strings.TrimSpace(argumentsJSON) != "null" {
		if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
			return "MCP TOOL REJECTED: invalid JSON arguments: " + err.Error(), nil
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	result, err := t.client.session.CallTool(ctx, &sdkmcp.CallToolParams{Name: t.name, Arguments: args})
	if err != nil {
		return "MCP TOOL FAILED: " + err.Error(), nil
	}
	text := renderResult(result)
	if result.IsError {
		return "MCP TOOL ERROR: " + text, nil
	}
	t.client.successful.Store(true)
	return text, nil
}

func renderResult(result *sdkmcp.CallToolResult) string {
	if result == nil {
		return "(empty MCP result)"
	}
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*sdkmcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err == nil {
			return string(encoded)
		}
	}
	return "(MCP tool returned no text)"
}
```

Do not write the result to PentGo's evidence directory in this task.

- [ ] **Step 5: Add soft-error coverage**

Register a second fixture tool in `TestMCPFixtureProcess`:

```go
mcp.AddTool(server, &mcp.Tool{Name: "fixture_error", Description: "Return a fixture tool error"},
	func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "fixture rejected"}},
			IsError: true,
		}, nil, nil
	})
```

Change the discovery assertion in `TestConnectStdioDiscoversAndInvokesTextTool` to `len(tools) != 2`, then add this helper and test:

```go
func invokableByName(t *testing.T, tools []tool.BaseTool, name string) tool.InvokableTool {
	t.Helper()
	for _, candidate := range tools {
		info, err := candidate.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == name {
			invokable, ok := candidate.(tool.InvokableTool)
			if !ok {
				t.Fatalf("tool %q type = %T, want InvokableTool", name, candidate)
			}
			return invokable
		}
	}
	t.Fatalf("tool %q was not discovered", name)
	return nil
}

func TestMCPToolErrorsAreSoftAndDoNotMarkSuccessfulCall(t *testing.T) {
	client, err := ConnectStdio(context.Background(), config.MCPConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPFixtureProcess"},
		Env:     map[string]string{"PENTGO_MCP_FIXTURE": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	errorResult, err := invokableByName(t, client.Tools(), "fixture_error").InvokableRun(context.Background(), `{}`)
	if err != nil || !strings.HasPrefix(errorResult, "MCP TOOL ERROR:") {
		t.Fatalf("tool error result/err = %q/%v", errorResult, err)
	}
	if client.HasSuccessfulCall() {
		t.Fatal("tool error marked the client as successful")
	}

	rejected, err := invokableByName(t, client.Tools(), "fixture_echo").InvokableRun(context.Background(), `{`)
	if err != nil || !strings.HasPrefix(rejected, "MCP TOOL REJECTED:") {
		t.Fatalf("malformed argument result/err = %q/%v", rejected, err)
	}
	if client.HasSuccessfulCall() {
		t.Fatal("malformed arguments marked the client as successful")
	}
}
```

- [ ] **Step 6: Run the MCP client tests**

Run:

```bash
go test ./internal/runtime/mcp -count=1
go test -race ./internal/runtime/mcp -count=1
```

Expected: PASS; the subprocess exits when `Client.Close` closes stdio.

- [ ] **Step 7: Commit the client boundary**

```bash
git add internal/runtime/mcp/client.go internal/runtime/mcp/client_test.go go.mod go.sum
git commit -m "feat: add minimal stdio mcp client"
```

---

### Task 4: Mount MCP Tools in the Existing Eino Agent Loop

**Files:**
- Modify: `internal/runtime/loop/runner.go:31-58`
- Modify: `internal/runtime/loop/eino_agent.go:24-50,118-124,397-446`
- Modify: `internal/runtime/loop/eino_run_loop.go:29-68`
- Modify: `internal/app/engagement.go:74-182`
- Create: `internal/app/engagement_mcp_test.go`

**Interfaces:**
- Consumes: `(*mcp.Client).Tools()`, `(*mcp.Client).HasSuccessfulCall()`, and `(*mcp.Client).Close()`.
- Produces: `RunnerConfig.MCPTools []tool.BaseTool` and `RunnerConfig.MCPEvidenceSeen func() bool` wired into the existing `complete_task` gate.

- [ ] **Step 1: Write the MCP-only full engagement test**

Create `internal/app/engagement_mcp_test.go` with the following local subprocess fixture, fake Eino model, helpers, and test. It reuses only helpers already defined in `engagement_test.go` in the same package:

```go
package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppMCPFixtureProcess(t *testing.T) {
	if os.Getenv("PENTGO_APP_MCP_FIXTURE") != "1" {
		return
	}
	type echoArgs struct {
		Value string `json:"value" jsonschema:"value to echo"`
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "pentgo-app-fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "fixture_echo", Description: "Echo a fixture value"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "fixture:" + args.Value}},
			}, nil, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type mcpCaptureModel struct {
	generated int
	tools     []*schema.ToolInfo
	inputs    [][]*schema.Message
}

func (m *mcpCaptureModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.tools = append([]*schema.ToolInfo(nil), tools...)
	return m, nil
}

func (m *mcpCaptureModel) Generate(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), messages...))
	m.generated++
	if m.generated == 1 {
		return einoToolCall("fixture_echo", `{"value":"TARGET"}`), nil
	}
	return einoComplete("MCP fixture returned evidence"), nil
}

func (m *mcpCaptureModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("streaming unsupported")
}

func containsToolInfo(tools []*schema.ToolInfo, name string) bool {
	for _, info := range tools {
		if info != nil && info.Name == name {
			return true
		}
	}
	return false
}

func messagesContain(inputs [][]*schema.Message, content string) bool {
	for _, messages := range inputs {
		for _, message := range messages {
			if message != nil && message.Content == content {
				return true
			}
		}
	}
	return false
}

func TestServiceCompletesWithSuccessfulMCPToolOnly(t *testing.T) {
	capture := &mcpCaptureModel{}
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "NO_FINDINGS"}},
		{response: agent.Response{Content: "NO_FINDINGS"}},
		{response: agent.Response{Content: "## 执行摘要\nMCP fixture completed.\n"}},
	}}
	cfg := config.Default()
	cfg.Agent.Provider = "anthropic"
	cfg.Agent.MCP = &config.MCPConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestAppMCPFixtureProcess"},
		Env:     map[string]string{"PENTGO_APP_MCP_FIXTURE": "1"},
	}
	service := NewService(cfg, Dependencies{
		Clock:           func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) },
		NewEngagementID: func(time.Time) (string, error) { return "eng-mcp-test", nil },
		NewAgentClient:  func(config.AgentConfig) (agent.Client, error) { return client, nil },
		NewEinoModel: func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) {
			return capture, nil
		},
	})

	result, err := service.Run(context.Background(), validRequest(t), nil)
	if err != nil || result.RunError != nil {
		t.Fatalf("result/err = %+v/%v", result, err)
	}
	if result.Session.Status != sess.SessionDone || result.Session.StopReason != "task_complete" {
		t.Fatalf("session = %+v", result.Session)
	}
	if !containsToolInfo(capture.tools, "fixture_echo") {
		t.Fatalf("mounted tools = %+v", capture.tools)
	}
	if !messagesContain(capture.inputs, "fixture:TARGET") {
		t.Fatalf("model inputs omit MCP result: %+v", capture.inputs)
	}
}
```

In `internal/runtime/loop/eino_run_loop_test.go`, add the import `github.com/cloudwego/eino/components/tool` and this focused reserved-name test:

```go
type namedMCPTool struct {
	name string
}

func (t namedMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t namedMCPTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "fixture", nil
}

func TestBuildToolsRejectsMCPToolNameCollision(t *testing.T) {
	tools := &einoToolSet{mcpTools: []tool.BaseTool{namedMCPTool{name: "exec"}}}
	_, err := tools.buildTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "conflicts with a PentGo tool") {
		t.Fatalf("buildTools error = %v, want reserved-name conflict", err)
	}
}
```

- [ ] **Step 2: Run the end-to-end test and verify MCP is not mounted**

Run:

```bash
go test ./internal/app -run TestServiceCompletesWithSuccessfulMCPToolOnly -count=1
go test ./internal/runtime/loop -run TestBuildToolsRejectsMCPToolNameCollision -count=1
```

Expected: both commands FAIL because `Service.Run` does not connect MCP, `einoToolSet` has no `mcpTools` field, and `buildTools` has no MCP collision handling.

- [ ] **Step 3: Extend RunnerConfig and the Eino tool set**

Add to `RunnerConfig`:

```go
MCPTools        []tool.BaseTool
MCPEvidenceSeen func() bool
```

Import `github.com/cloudwego/eino/components/tool` in `runner.go`.

Add matching fields to `einoToolSet` and populate them in `RunEino`:

```go
mcpTools        []tool.BaseTool
mcpEvidenceSeen func() bool
```

```go
mcpTools:        runner.config.MCPTools,
mcpEvidenceSeen: runner.config.MCPEvidenceSeen,
```

Extend the existing gate check without changing local evidence semantics:

```go
func (ts *einoToolSet) evidenceSeen() bool {
	ts.mu.Lock()
	local := ts.hasEvidence
	ts.mu.Unlock()
	if local {
		return true
	}
	return ts.mcpEvidenceSeen != nil && ts.mcpEvidenceSeen()
}
```

- [ ] **Step 4: Append MCP tools with collision validation**

After building the local tools in `buildTools`, validate and append the configured MCP tools:

```go
builtins := []tool.BaseTool{execTool, pythonTool, skillTool, sessionTool, gateTool}
reserved := map[string]bool{
	"exec": true, "execute_python": true, "load_skill": true,
	"declare_session": true, evidenceGateToolName: true, completeTaskToolName: true,
}
for _, external := range ts.mcpTools {
	if external == nil {
		return nil, fmt.Errorf("nil mcp tool")
	}
	info, err := external.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("read mcp tool info: %w", err)
	}
	if info == nil || strings.TrimSpace(info.Name) == "" {
		return nil, fmt.Errorf("mcp tool has empty name")
	}
	if reserved[info.Name] {
		return nil, fmt.Errorf("mcp tool %q conflicts with a PentGo tool", info.Name)
	}
	reserved[info.Name] = true
	builtins = append(builtins, external)
}
return builtins, nil
```

Change the method signature to `buildTools(ctx context.Context)` and call it from `newEinoAgent` as `tools.buildTools(ctx)`.

- [ ] **Step 5: Connect and close MCP in Service.Run**

Import `pentgo/internal/runtime/mcp` with an explicit alias. In `Service.Run`, place this block after the existing `agentConfig := service.cfg.Agent` assignment and before constructing the executor and runner:

```go
var externalMCP *runtimemcp.Client
if agentConfig.MCP != nil {
	connectTimeout := time.Duration(agentConfig.RequestTimeoutSeconds) * time.Second
	if connectTimeout <= 0 {
		connectTimeout = time.Minute
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, connectTimeout)
	externalMCP, err = runtimemcp.ConnectStdio(connectCtx, *agentConfig.MCP)
	cancelConnect()
	if err != nil {
		return result, fmt.Errorf("initialize mcp: %w", err)
	}
	defer externalMCP.Close()
}
```

Build the two runner values only when the client exists:

```go
var mcpTools []tool.BaseTool
var mcpEvidenceSeen func() bool
if externalMCP != nil {
	mcpTools = externalMCP.Tools()
	mcpEvidenceSeen = externalMCP.HasSuccessfulCall
}
```

Pass them in `RunnerConfig`:

```go
MCPTools:        mcpTools,
MCPEvidenceSeen: mcpEvidenceSeen,
```

Import Eino's `tool` package in `engagement.go` for the local slice type. The deferred close is the only lifecycle management in this prototype.

- [ ] **Step 6: Run integration and regression tests**

Run:

```bash
go test ./internal/runtime/loop ./internal/runtime/mcp ./internal/app -count=1
```

Expected: PASS. The MCP-only engagement must reach `done/task_complete`, and all local `exec`/`execute_python` tests must remain green.

- [ ] **Step 7: Commit the mounted tool path**

```bash
git add internal/runtime/loop/runner.go internal/runtime/loop/eino_agent.go internal/runtime/loop/eino_run_loop.go internal/app/engagement.go internal/app/engagement_mcp_test.go
git commit -m "feat: mount stdio mcp tools in agent"
```

---

### Task 5: Update Agent Guidance and Documentation

**Files:**
- Modify: `internal/runtime/loop/prompt.go`
- Modify: `internal/runtime/loop/prompt_content_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Consumes: final tool names and single-server configuration from Tasks 1-4.
- Produces: stable model guidance and operator documentation for the prototype.

- [ ] **Step 1: Write the failing prompt-contract test**

Add:

```go
func TestToolPromptExplainsSpecializedMCPAndLocalExecution(t *testing.T) {
	prompt := buildOpenAISystemPrompt(nil)
	for _, want := range []string{
		"specialized MCP tool", "exec", "execute_python", "complete_task",
		"Prefer", "engagement work directory",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("tool prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "execute_code") {
		t.Fatalf("tool prompt retains removed execute_code contract: %q", prompt)
	}
}
```

- [ ] **Step 2: Run the prompt test and verify old guidance remains**

Run:

```bash
go test ./internal/runtime/loop -run TestToolPromptExplainsSpecializedMCPAndLocalExecution -count=1
```

Expected: FAIL because the prompt still advertises `execute_code`.

- [ ] **Step 3: Replace the native-tool guidance**

In `openAIToolSystemPrompt`, replace the old `execute_code` paragraph with:

```text
=== YOUR TOOLS ===
- Prefer a registered specialized MCP tool when it directly matches the operation. Use its exact name and schema.
- exec(command): run installed commands, Bash pipelines, redirection, and short system operations in the engagement work directory.
- execute_python(script): run a complete Python program for temporary request logic, parsing, batching, and custom analysis. Print decision-grade observations to stdout.
- Do not reimplement an available specialized MCP tool wholesale in exec or execute_python. Use local execution when composition or missing capability requires it.
- load_skill(name): load registered read-only guidance.
- declare_session(...): establish and verify an authenticated identity.
- complete_task(final_result): finish only after a successful local or MCP tool result supports the conclusion.
```

Update every native-tool reference in the same prompt from `execute_code` to the appropriate `exec`/`execute_python` wording. Leave `baseSystemPrompt` code-fence instructions unchanged because it serves the retained text-provider fallback.

- [ ] **Step 4: Update README configuration and runtime description**

Add the optional block under `agent`:

```json
"mcp": {
  "command": "/absolute/path/to/mcp-server",
  "args": ["--stdio"],
  "env": {
    "FIXTURE_TOKEN": "VALUE"
  }
}
```

Document the runtime tool policy:

```text
专用 MCP 工具优先；系统命令和已有 CLI 使用 exec；临时 Python 编排、解析和批处理使用 execute_python。
```

Correct the provider section so it states that OpenAI and Anthropic both use Eino native tool calls. State the prototype boundaries: one stdio server, process lifetime equals one engagement, no transport reconnect or management UI.

- [ ] **Step 5: Update the architecture map**

Add `internal/runtime/mcp` between configuration and Eino `ToolsNode`:

```text
agent.mcp config
  -> runtime/mcp ConnectStdio + tools/list
  -> Eino BaseTool adapters
  -> existing ChatModelAgent ToolsNode
  -> tools/call -> stdio subprocess
```

State explicitly that local `exec` and `execute_python` bypass an internal MCP server and continue to use PentGo's executor directly.

- [ ] **Step 6: Run prompt and documentation-adjacent tests**

Run:

```bash
go test ./internal/runtime/loop ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit guidance and documentation**

```bash
git add internal/runtime/loop/prompt.go internal/runtime/loop/prompt_content_test.go README.md docs/ARCHITECTURE.md
git commit -m "docs: describe mcp and split execution tools"
```

---

### Task 6: Full Verification and Scope Audit

**Files:**
- Verify only; make fixes in the owning files from Tasks 1-5 if a command exposes a regression.

**Interfaces:**
- Consumes: the complete implementation.
- Produces: a buildable, race-checked prototype whose changed surface matches the global constraints.

- [ ] **Step 1: Confirm the removed public tool name is gone from production native-tool code**

Run:

```bash
rg -n 'execute_code' internal/runtime/loop internal/app README.md docs/ARCHITECTURE.md
```

Expected: no production native-tool references. Historical text-fallback comments or archived plan files outside these paths are outside this check.

- [ ] **Step 2: Confirm excluded MCP features were not introduced**

Run:

```bash
rg -n 'Streamable|SSE|HTTPTransport|reconnect|circuit|execution_id|RBAC|HITL' internal/runtime/mcp internal/app/engagement.go
```

Expected: no matches in the new MCP path.

- [ ] **Step 3: Run the complete test suite**

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run race detection**

```bash
go test -race ./...
```

Expected: PASS, including concurrent Eino tool execution and the MCP client's atomic success state.

- [ ] **Step 5: Run static and dependency verification**

```bash
go vet ./...
go build ./...
go mod verify
```

Expected: all commands exit 0.

- [ ] **Step 6: Review the final diff against the prototype boundary**

Run:

```bash
git diff --stat HEAD~5..HEAD
git diff HEAD~5..HEAD -- internal/runtime/mcp internal/runtime/loop/eino_agent.go internal/app/engagement.go internal/config/config.go
```

Expected: the diff contains the two local tools, one stdio client, one service integration, tests, and documentation only. Evidence/report formats and verification packages remain unchanged.

- [ ] **Step 7: Confirm verification left no pending correction**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` prints nothing and exits 0. `git status --short` contains no implementation correction; Tasks 1-5 already committed their own files. If verification exposed a defect, fix it in the owning task, rerun that task's focused test and commit command, then repeat Steps 1-7.
