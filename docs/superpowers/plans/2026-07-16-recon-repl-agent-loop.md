# Recon REPL Agent Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 PentGo 收束为自然语言 REPL 入口，并在 Recon 阶段实现 OpenAI/Anthropic 驱动的“结构化 Skill 选择 -> 本地执行 -> 证据回灌”循环。

**Architecture:** `internal/agent` 负责把统一的 Agent 请求编码为 OpenAI 或 Anthropic 原生 tool use 请求；`internal/recon` 负责校验模型选择、执行已注册 Skill、记录 Decision/Call/evidence，并生成下一轮上下文。`internal/app` 负责创建 engagement 和发布 artifacts，`internal/terminal` 负责 REPL、自然语言 target 提取和当前 engagement 的 `Ctrl+C` 取消。

**Tech Stack:** Go 1.25、标准库 `net/http`/`encoding/json`/`embed`/`os/signal`、现有本地 Skill 执行器、OpenAI Chat Completions tools、Anthropic Messages tools。

## Global Constraints

- Go 版本保持 `1.25.0`，不新增第三方依赖。
- 所有新增 Go 注释使用中文。
- 所有 `*_test.go` 物理文件放在 `tests/`；沿用仓库现有的测试镜像/符号链接布局。
- REPL 是唯一 Recon 执行入口；只保留`pentgo --help` 与 `pentgo --version` 仅输出说明。
- 模型只能调用已注册 Recon Skill；执行器从不执行模型生成的 Python、Bash、命令路径或任意 shell 参数。
- 每次已执行 Skill 继续写一个独立 JSON evidence 文件；凭据不进入 session、报告、日志或 evidence。
- `recon.agent.enabled=false` 时保留当前固定 Recon 路径；启用时模型协议、参数校验和轮次超限错误终止 Recon。
- 已注册 Skill 返回 `failed` 或 `skipped` 时写证据并回灌下一轮；只有执行器内部错误、evidence 写入错误、模型调用错误或解析错误终止 Recon。

---

## File Structure

| 路径 | 责任 |
| --- | --- |
| `internal/config/config.go` | 定义并规范化 `recon.agent` 配置。 |
| `internal/agent/types.go` | Provider 无关的请求、工具定义、决策和客户端接口。 |
| `internal/agent/openai.go` | OpenAI Chat Completions tool-call 编码与解析。 |
| `internal/agent/anthropic.go` | Anthropic Messages tool-use 编码与解析。 |
| `skills/recon/SKILL.md` | 内嵌给模型的 Recon 方法论和完成规则。 |
| `skills/registry.go` | 使用 `go:embed` 提供阶段 Skill Prompt。 |
| `internal/recon/agent_catalog.go` | 将被动 Provider、原生 Collector 和本地 Recon Skill 适配为模型可选动作。 |
| `internal/recon/agent_runner.go` | 单动作 Agent Loop、Decision/Call/evidence 状态迁移。 |
| `internal/recon/bingo_skills.go` | 保留固定 Tscan 链，同时新增细粒度 Tscan 动作构造器。 |
| `internal/recon/types.go`, `internal/recon/state.go`, `internal/recon/runner.go` | 支持模型来源 Decision 和可选 Agent Runner。 |
| `internal/redteam/session.go`, `internal/redteam/recon_pipeline.go` | 持久化自然语言 intent，并将它送入 Recon Runner。 |
| `internal/report/markdown.go` | 在报告开头展示 intent。 |
| `internal/app/engagement.go` | 创建 session/writer、组装 Runner、运行并发布 engagement。 |
| `internal/terminal/terminal.go` | REPL、命令、自然语言 target 提取、状态显示和取消。 |
| `cmd/pentgo/main.go` | 仅保留 help/version/REPL 分发和信号路由。 |
| `README.md` | 更新为 REPL 用法、Agent 配置和输出结构。 |

## Task 1: Add Agent Configuration

**Files:**
- Modify: `internal/config/config.go:14-153`
- Modify: `tests/_packages/internal/config/config_test.go:12-132`

**Interfaces:**
- Produces `config.AgentConfig`、`config.ModelProviderConfig`。
- `config.ReconConfig.Agent` 供 `internal/app` 选择固定 Runner 或 Agent Runner。

- [ ] **Step 1: Write failing configuration tests**

```go
func TestDefaultIncludesDisabledReconAgent(t *testing.T) {
	agent := Default().Recon.Agent
	if agent.Enabled || agent.Provider != "openai" || agent.MaxTurns != 12 || agent.TimeoutSeconds != 60 {
		t.Fatalf("agent default = %+v", agent)
	}
	if agent.OpenAI.BaseURL != "https://api.openai.com/v1" || agent.OpenAI.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("openai default = %+v", agent.OpenAI)
	}
	if agent.Anthropic.BaseURL != "https://api.anthropic.com" || agent.Anthropic.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("anthropic default = %+v", agent.Anthropic)
	}
}

func TestLoadMergesReconAgentConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG configuration path is only used on Linux and WSL")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := ConfigFile()
	if err != nil { t.Fatal(err) }
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
	data := []byte(`{"recon":{"agent":{"enabled":true,"provider":"anthropic","max_turns":3,"anthropic":{"model":"fixture-model"}}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	cfg, err := Load()
	if err != nil { t.Fatal(err) }
	if !cfg.Recon.Agent.Enabled || cfg.Recon.Agent.Provider != "anthropic" || cfg.Recon.Agent.MaxTurns != 3 || cfg.Recon.Agent.TimeoutSeconds != 60 {
		t.Fatalf("agent = %+v", cfg.Recon.Agent)
	}
	if cfg.Recon.Agent.Anthropic.Model != "fixture-model" || cfg.Recon.Agent.Anthropic.BaseURL != "https://api.anthropic.com" || cfg.Recon.Agent.Anthropic.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("anthropic = %+v", cfg.Recon.Agent.Anthropic)
	}
}
```

- [ ] **Step 2: Run the configuration tests to verify they fail**

Run: `go test ./internal/config -run 'TestDefaultIncludesDisabledReconAgent|TestLoadMergesReconAgentConfiguration' -count=1`

Expected: FAIL，因为 `ReconConfig.Agent` 尚未定义。

- [ ] **Step 3: Add the config model and normalization**

```go
// AgentConfig 描述 Recon 结构化 Skill Agent Loop 的模型与轮次限制。
type AgentConfig struct {
	Enabled        bool                `json:"enabled"`
	Provider       string              `json:"provider"`
	MaxTurns       int                 `json:"max_turns"`
	TimeoutSeconds int                 `json:"timeout_seconds"`
	OpenAI         ModelProviderConfig `json:"openai"`
	Anthropic      ModelProviderConfig `json:"anthropic"`
}

// ModelProviderConfig 描述一个原生模型协议的连接信息。
type ModelProviderConfig struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
}
```

将 `Agent AgentConfig` 加入 `ReconConfig`，在 `defaultReconConfig` 中设置 `Enabled:false`、`Provider:"openai"`、`MaxTurns:12`、`TimeoutSeconds:60`、两个默认 base URL/API Key 环境变量。新增 `normalizeAgentConfig`，仅补齐空 provider、非正轮次/超时、空 base URL 和空 API Key 环境变量；`Model` 保持空值，以便启用 Agent 时由运行时验证并给出明确错误。

- [ ] **Step 4: Run the configuration tests to verify they pass**

Run: `go test ./internal/config -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the configuration task**

```bash
git add internal/config/config.go tests/_packages/internal/config/config_test.go
git commit -m "feat: add recon agent configuration"
```

## Task 2: Implement Provider-Neutral Agent Clients

**Files:**
- Create: `internal/agent/types.go`
- Create: `internal/agent/openai.go`
- Create: `internal/agent/anthropic.go`
- Create: `tests/_packages/internal/agent/client_test.go`
- Create: `internal/agent/client_test.go` (symbolic link to `../../tests/_packages/internal/agent/client_test.go`)

**Interfaces:**
- Consumes: `config.ModelProviderConfig` values after `internal/app` maps them to `agent.ProviderConfig`.
- Produces: `agent.Client.Decide(context.Context, agent.Request) (agent.Decision, error)`.

- [ ] **Step 1: Write failing OpenAI and Anthropic HTTP contract tests**

```go
func TestOpenAIClientDecidesFromSingleToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("request = %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tool_choice"] != "required" || body["parallel_tool_calls"] != false {
			t.Fatalf("body = %#v", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"dns","arguments":"{\"reason\":\"resolve host\"}"}}]}}]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(ProviderConfig{BaseURL: server.URL + "/v1", Model: "fixture", APIKeyEnv: "TEST_KEY"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	request := Request{SystemPrompt: "system", UserPrompt: "user", Tools: []ToolDefinition{{ID: "dns", Description: "resolve", Parameters: map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string"}}, "required": []string{"reason"}}}}}
	decision, err := client.Decide(context.Background(), request)
	if err != nil || decision.ToolID != "dns" || decision.Arguments["reason"] != "resolve host" {
		t.Fatalf("decision/err = %+v/%v", decision, err)
	}
}

func TestAnthropicClientDecidesFromToolUseBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Fatalf("request = %s %q %q", r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version"))
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","name":"dns","input":{"reason":"resolve host"}}]}`)
	}))
	defer server.Close()
	client := NewAnthropicClient(ProviderConfig{BaseURL: server.URL, Model: "fixture", APIKeyEnv: "TEST_KEY"}, server.Client(), func(string) (string, bool) { return "test-key", true })
	decision, err := client.Decide(context.Background(), Request{SystemPrompt: "system", UserPrompt: "user", Tools: []ToolDefinition{{ID: "dns", Description: "resolve", Parameters: map[string]any{"type": "object"}}}})
	if err != nil || decision.ToolID != "dns" || decision.Arguments["reason"] != "resolve host" {
		t.Fatalf("decision/err = %+v/%v", decision, err)
	}
}
```

- [ ] **Step 2: Run provider tests to verify they fail**

Run: `go test ./internal/agent -run 'TestOpenAIClient|TestAnthropicClient' -count=1`

Expected: FAIL，因为 `internal/agent` 和客户端尚不存在。

- [ ] **Step 3: Define the shared agent contract**

```go
// Client 定义模型在单个 Recon 回合选择一个动作的边界。
type Client interface {
	Decide(context.Context, Request) (Decision, error)
}

type Request struct {
	SystemPrompt string
	UserPrompt   string
	Tools        []ToolDefinition
}

type ToolDefinition struct {
	ID          string
	Description string
	Parameters  map[string]any
}

type Decision struct {
	ToolID   string
	Arguments map[string]any
}
```

`ProviderConfig` 只保存 base URL、model 和 API Key 环境变量。每个客户端在 `Decide` 中读取环境变量、创建带 context 的 HTTP 请求、限制响应体读取大小，并在非 2xx、空 choices/content、多个 tool call、未知 JSON 或无效 arguments 时返回描述性错误。

- [ ] **Step 4: Implement native request/response mapping**

OpenAI payload 必须使用 `POST {base_url}/chat/completions`、`Authorization: Bearer`、`messages`、`tools`、`tool_choice:"required"` 和 `parallel_tool_calls:false`。将唯一的 `message.tool_calls[0].function.name/arguments` 解析为 `Decision`。

Anthropic payload 必须使用 `POST {base_url}/v1/messages`、`x-api-key`、`anthropic-version: 2023-06-01`、`system`、一个 user message、`tools`、`tool_choice:{"type":"any","disable_parallel_tool_use":true}`。将唯一的 `content.type:"tool_use"` block 的 `name/input` 解析为 `Decision`。

- [ ] **Step 5: Add protocol failure tests and run the package**

Add these exact cases: `TestOpenAIClientRejectsMissingAPIKey` passes an env lookup returning `(\"\", false)` and asserts an error containing `API key`; `TestOpenAIClientRejectsMultipleToolCalls` returns two tool calls and asserts an error containing `exactly one`; `TestAnthropicClientRejectsHTTPError` returns HTTP 429 and asserts an error containing `429`; `TestAnthropicClientRejectsMalformedToolInput` returns `input:\"invalid\"` and asserts an error containing `tool input`. Run:

```bash
go test ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the provider task**

```bash
git add internal/agent tests/_packages/internal/agent
git commit -m "feat: add recon agent model clients"
```

## Task 3: Add Recon Knowledge and a Registered Action Catalog

**Files:**
- Create: `skills/recon/SKILL.md`
- Create: `skills/registry.go`
- Create: `internal/recon/agent_catalog.go`
- Modify: `internal/recon/bingo_skills.go:99-252`
- Create: `tests/_packages/skills/registry_test.go`
- Create: `skills/registry_test.go` (symbolic link to `../tests/_packages/skills/registry_test.go`)
- Create: `tests/packages/internal/recon/agent_catalog.go` (symbolic link to `../../../../internal/recon/agent_catalog.go`)
- Create: `tests/packages/internal/recon/agent_catalog_test.go`

**Interfaces:**
- Consumes: existing `PassiveProvider`、`NativeCollector`、`ReconSkill` 和 `skill.Executor`.
- Produces: `recon.AgentCatalog` with `Definitions() []agent.ToolDefinition` and `Action(id string) (AgentAction, bool)`.

- [ ] **Step 1: Write failing knowledge and catalog tests**

```go
func TestPhasePromptReturnsEmbeddedReconKnowledge(t *testing.T) {
	prompt, ok := skills.PhasePrompt("recon")
	if !ok || !strings.Contains(prompt, "recon_complete") || !strings.Contains(prompt, "证据") {
		t.Fatalf("prompt/ok = %q/%t", prompt, ok)
	}
}

func TestAgentCatalogExposesOnlyRegisteredActions(t *testing.T) {
	catalog := NewAgentCatalog(
		[]PassiveProvider{catalogPassiveProvider{}},
		[]NativeCollector{catalogNativeCollector{}},
		[]ReconSkill{catalogReconSkill{id: "dns"}},
	)
	if _, ok := catalog.Action("dns"); !ok {
		t.Fatal("dns action is missing")
	}
	if _, ok := catalog.Action("unregistered"); ok {
		t.Fatal("unregistered action is available")
	}
}
```

Define `catalogPassiveProvider` with `Name() string { return "fofa" }`, a `Plan` returning a passive `fofa` call, and a skipped `Collect`; define `catalogNativeCollector` with `Name() string { return HTTPMetadataCollectorName }` and a skipped `Collect`; define `catalogReconSkill.Run` returning a skipped `SkillResult`. These fixtures make catalog construction deterministic and network-free.

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./skills ./tests/packages/internal/recon -run 'TestPhasePrompt|TestAgentCatalog' -count=1`

Expected: FAIL，因为嵌入 Skill 与 AgentCatalog 尚不存在。

- [ ] **Step 3: Add the embedded Recon Skill prompt**

Create a concise Chinese `skills/recon/SKILL.md` that requires evidence-led decisions, names only the registered actions, states one action per turn, explains that failed/skipped results are still evidence, and requires `recon_complete` when the available evidence is sufficient. Implement:

```go
package skills

import _ "embed"

//go:embed recon/SKILL.md
var reconPrompt string

func PhasePrompt(phase string) (string, bool) {
	if phase != "recon" {
		return "", false
	}
	return reconPrompt, true
}
```

- [ ] **Step 4: Add action interfaces and adapters**

```go
type AgentAction interface {
	Definition() agent.ToolDefinition
	Repeatable() bool
	Plan(SkillInput, map[string]any) (PlannedCall, error)
	Run(context.Context, SkillInput, map[string]any) AgentActionResult
	EvidenceName(Call) string
	Evidence(Call, AgentActionResult, time.Time) any
}

type AgentActionResult struct {
	Outcome CallOutcome
	Evidence any
}
```

Implement adapters for FOFA、Shodan、HTTP metadata、DNS、subfinder and local command Skills. Existing `PassiveEvidence`、`NativeEvidence` 和 `SkillEvidence` remain the payload types selected by their matching adapters. Each action schema includes a required string `reason`; action-specific input remains empty in the first version, so the validated session target always determines host, URL and executable arguments.

Split Tscan construction into `tscan_domain`、`tscan_port`、`tscan_url`、`tscan_poc`、`tscan_dir` 和 `tscan_js` actions. Keep the existing `tscan` composite Skill untouched for the disabled-Agent fixed path.

- [ ] **Step 5: Add catalog validation tests and run them**

Add assertions that each Tscan module uses a fixed `-m MODULE` argument, IP targets exclude `tscan_domain`, arbitrary action arguments are rejected, and each action is non-repeatable. Run:

```bash
go test ./skills ./tests/packages/internal/recon -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the catalog task**

```bash
git add skills internal/recon/bingo_skills.go internal/recon/agent_catalog.go tests/_packages/skills tests/packages/internal/recon
git commit -m "feat: register recon agent skills"
```

## Task 4: Implement the Agent Recon Runner and State Integration

**Files:**
- Create: `internal/recon/agent_runner.go`
- Modify: `internal/recon/types.go:82-192`
- Modify: `internal/recon/state.go:25-540`
- Modify: `internal/recon/runner.go:11-99`
- Modify: `internal/redteam/session.go:42-98`
- Modify: `internal/redteam/recon_pipeline.go:71-90`
- Modify: `internal/report/markdown.go:14-31`
- Create: `tests/packages/internal/recon/agent_runner.go` (symbolic link to `../../../../internal/recon/agent_runner.go`)
- Create: `tests/packages/internal/recon/agent_runner_test.go`
- Modify: `tests/_packages/internal/redteam/recon_pipeline_test.go`
- Modify: `tests/_packages/internal/redteam/session_state_test.go`

**Interfaces:**
- Consumes: `agent.Client`、`AgentCatalog`、`skills.PhasePrompt("recon")`、`recon.State` and `Session.Intent`.
- Produces: model sourced `Decision` records, per-action evidence, a final stop decision, and a completed/failing Recon state.

- [ ] **Step 1: Write failing AgentRunner behavior tests**

```go
func TestAgentRunnerExecutesTwoDecisionsThenCompletes(t *testing.T) {
	client := scriptedClient{
		{ToolID: "dns", Arguments: map[string]any{"reason": "resolve"}},
		{ToolID: "http_metadata", Arguments: map[string]any{"reason": "fingerprint"}},
		{ToolID: "recon_complete", Arguments: map[string]any{"summary": "baseline complete"}},
	}
	catalog := NewAgentCatalog(nil, []NativeCollector{catalogNativeCollector{}}, []ReconSkill{catalogReconSkill{id: "dns"}})
	runner := NewAgentRunner(client, catalog, &memoryEvidenceSink{paths: []string{}}, 3, fixedClock)
	state := NewState()
	if err := runner.Run(context.Background(), state, AgentInput{Host: "example.test", Target: "https://example.test", Intent: "资产侦察"}); err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot()
	if snapshot.Status != StateStatusDone || len(snapshot.Calls) != 2 || snapshot.Decisions[len(snapshot.Decisions)-1].Source != DecisionSourceModel {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestAgentRunnerFeedsFailedSkillResultBackToModel(t *testing.T) {
	client := scriptedClient{
		{ToolID: "dns", Arguments: map[string]any{"reason": "resolve"}},
		{ToolID: "recon_complete", Arguments: map[string]any{"summary": "no more actions"}},
	}
	catalog := NewAgentCatalog(nil, nil, []ReconSkill{catalogReconSkill{id: "dns", result: SkillResult{SkillID: "dns", Status: CallStatusFailed, Summary: "resolver error"}}})
	if err := NewAgentRunner(client, catalog, &memoryEvidenceSink{}, 2, fixedClock).Run(context.Background(), NewState(), AgentInput{Host: "example.test", Target: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2", client.calls)
	}
}

func TestAgentRunnerRejectsRepeatedOrUnknownAction(t *testing.T) {
	// unknown 和重复 non-repeatable action 都必须使 state/status 进入 failed。
}
```

- [ ] **Step 2: Run the AgentRunner tests to verify they fail**

Run: `go test ./tests/packages/internal/recon -run 'TestAgentRunner' -count=1`

Expected: FAIL，因为 AgentRunner 和 `DecisionSourceModel` 尚不存在。

- [ ] **Step 3: Extend state and session types**

Add `DecisionSourceModel = "model"`; allow model decisions with `BatchKindSkill`; allow a model-driven first Skill batch in `StartBatch`; retain the existing first-passive requirement for fixed decisions. Add `Intent string 'json:"intent,omitempty"'` to `redteam.Session` and add `NewEngagementSessionForIntent` that delegates to the existing constructor and assigns a trimmed intent.

Update `renderMarkdown` to emit `- Intent: ...` when present. Keep model completion reason in the final stop decision summary and append it to the Recon phase summary.

- [ ] **Step 4: Implement the bounded Agent Loop**

```go
type AgentInput struct {
	Host   string
	Target string
	Intent string
}

func (r *AgentRunner) Run(ctx context.Context, state *State, input AgentInput) error {
	for turn := 0; turn < r.maxTurns; turn++ {
		decision, err := r.client.Decide(ctx, r.buildRequest(state.Snapshot(), input))
		if err != nil { return r.fail(state, fmt.Errorf("decide Recon action: %w", err)) }
		if decision.ToolID == "recon_complete" { return r.complete(state, decision, input) }
		if err := r.executeOne(ctx, state, input, decision); err != nil { return err }
	}
	return r.fail(state, fmt.Errorf("Recon agent exceeded %d turns without recon_complete", r.maxTurns))
}
```

`buildRequest` must include the embedded phase prompt, original intent, canonical target, only currently available actions, and bounded call/observation/evidence summaries. `executeOne` records a `DecisionSourceModel` decision before execution, then creates one Batch/Call, writes one evidence file, finishes the Call/Batch, and returns to the model for every non-cancelled terminal Skill result. `recon_complete` must reject unknown arguments and record a stop decision before `State.Complete`.

Modify `Runner` so it accepts optional `AgentRunner` and an `intent` argument. It dispatches to `AgentRunner.Run` only when configured; current passive/native/fixed Skill execution remains intact when it is nil.

- [ ] **Step 5: Verify pipeline and report integration**

Add tests proving `Session.Intent` survives JSON round-tripping, the Recon phase summary includes the final model completion summary, and the Markdown report shows intent plus `model` Decisions. Run:

```bash
go test ./tests/packages/internal/recon ./internal/redteam ./internal/report -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the Agent Runner task**

```bash
git add internal/recon internal/redteam internal/report tests/packages/internal/recon tests/_packages/internal/redteam
git commit -m "feat: run recon through structured agent loop"
```

## Task 5: Extract the Shared Engagement Application Service

**Files:**
- Create: `internal/app/engagement.go`
- Create: `tests/_packages/internal/app/engagement_test.go`
- Create: `internal/app/engagement_test.go` (symbolic link to `../../tests/_packages/internal/app/engagement_test.go`)

**Interfaces:**
- Consumes: `config.Config`、`redteam.Target`、intent、output root and a progress callback.
- Produces: `app.Result{Session, Artifacts, RunError}` after artifacts are published for success, failure or cancellation.

- [ ] **Step 1: Write failing application-service tests**

```go
func TestServicePublishesArtifactsAfterCancelledEngagement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target, err := redteam.ParseTarget("https://example.test")
	if err != nil { t.Fatal(err) }
	service := NewService(config.Default(), Dependencies{
		Clock: fixedClock,
		NewEngagementID: func(time.Time) (string, error) { return "eng-service-test", nil },
	})
	result, err := service.Run(ctx, Request{Target: target, Intent: "侦察", OutputRoot: t.TempDir()})
	if err != nil || result.RunError == nil || result.Artifacts.SessionJSON == "" {
		t.Fatalf("result/err = %+v/%v", result, err)
	}
	if _, statErr := os.Stat(result.Artifacts.Markdown); statErr != nil {
		t.Fatal(statErr)
	}
}
```

- [ ] **Step 2: Run the service test to verify it fails**

Run: `go test ./internal/app -run TestServicePublishesArtifactsAfterCancelledEngagement -count=1`

Expected: FAIL，因为 `internal/app` 尚不存在。

- [ ] **Step 3: Implement `app.Service`**

Move engagement ID generation and the orchestration formerly in `runScan` into:

```go
type Request struct {
	Target     redteam.Target
	Intent     string
	OutputRoot string
}

type Result struct {
	Session   *redteam.Session
	Artifacts report.Artifacts
	RunError  error
}

func (s *Service) Run(ctx context.Context, request Request, progress func(Event)) (Result, error)
```

`Dependencies` contains `Clock func() time.Time` and `NewEngagementID func(time.Time) (string, error)`; production construction supplies UTC wall-clock and the existing cryptographically random ID generator, while tests supply deterministic functions.

Build passive/native/local Skill dependencies exactly as today. When `cfg.Recon.Agent.Enabled` is true, validate the selected provider model/API key configuration, construct `agent.Client`, build the AgentCatalog, and pass an AgentRunner into `recon.Runner`. Always call `writer.Publish` after `pipeline.Run`; reserve the outer `error` result for setup or artifact publication failures and put a Pipeline failure/cancellation in `Result.RunError`.

- [ ] **Step 4: Run service and existing E2E tests**

Run:

```bash
go test ./internal/app -count=1
```

Expected: PASS while `cmd/pentgo` still uses its existing flow; CLI wiring changes in Task 6.

- [ ] **Step 5: Commit the service task**

```bash
git add internal/app tests/_packages/internal/app
git commit -m "refactor: extract engagement application service"
```

## Task 6: Build the Natural-Language REPL and Simplify the CLI

**Files:**
- Create: `internal/terminal/terminal.go`
- Create: `tests/_packages/internal/terminal/terminal_test.go`
- Create: `internal/terminal/terminal_test.go` (symbolic link to `../../tests/_packages/internal/terminal/terminal_test.go`)
- Modify: `cmd/pentgo/main.go:20-184`
- Modify: `tests/_packages/cmd/pentgo/main_test.go`
- Modify: `tests/_packages/cmd/pentgo/e2e_test.go`

**Interfaces:**
- Consumes: `app.Service` through a small `EngagementRunner` interface and an input/output/signal abstraction.
- Produces: one serialized REPL session where each natural language line either starts one engagement or reports a parsing error.

- [ ] **Step 1: Write failing REPL tests**

```go
type recordingRunner struct {
	requests []app.Request
	result   app.Result
}

func (r *recordingRunner) Run(_ context.Context, request app.Request, _ func(app.Event)) (app.Result, error) {
	r.requests = append(r.requests, request)
	return r.result, nil
}

func TestParseTaskExtractsURLAndRetainsIntent(t *testing.T) {
	task, err := ParseTask("对 https://Example.Test/app 做资产侦察，优先子域名")
	if err != nil || task.Target.Origin != "https://example.test:443" || task.Intent == "" {
		t.Fatalf("task/err = %+v/%v", task, err)
	}
}

func TestTerminalRunsNaturalLanguageTaskAndReturnsToPrompt(t *testing.T) {
	runner := &recordingRunner{result: app.Result{Artifacts: report.Artifacts{Markdown: "eng-test/report.md", SessionJSON: "eng-test/session.json"}}}
	var output bytes.Buffer
	terminal := New(strings.NewReader("对 example.test 做侦察\n/quit\n"), &output, runner, make(chan os.Signal))
	if err := terminal.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 1 || !strings.Contains(output.String(), "report.md") {
		t.Fatalf("requests/output = %+v/%q", runner.requests, output.String())
	}
}

func TestTerminalCancelsActiveEngagementOnInterrupt(t *testing.T) {
	signals := make(chan os.Signal, 1)
	runner := newBlockingRunner()
	var output bytes.Buffer
	terminal := New(strings.NewReader("对 example.test 做侦察\n/quit\n"), &output, runner, signals)
	done := make(chan error, 1)
	go func() { done <- terminal.Run(context.Background()) }()
	<-runner.started
	signals <- os.Interrupt
	if err := <-done; err != nil { t.Fatal(err) }
	if !runner.cancelled || !strings.Contains(output.String(), "cancelled") {
		t.Fatalf("cancelled/output = %t/%q", runner.cancelled, output.String())
	}
}
```

Define `blockingRunner` in the same test file with a `started chan struct{}` and a `Run` method that closes `started`, waits on `ctx.Done()`, records `cancelled=true`, then returns `app.Result{Artifacts: report.Artifacts{Markdown:"eng-cancel/report.md"}, RunError:ctx.Err()}`. This gives the cancellation test a concrete synchronization point without a network call.

- [ ] **Step 2: Run terminal tests to verify they fail**

Run: `go test ./internal/terminal -count=1`

Expected: FAIL，因为 `internal/terminal` 尚不存在。

- [ ] **Step 3: Implement task parsing and the serialized REPL**

`ParseTask` scans the first HTTP(S) URL, then falls back to a bounded bare-domain pattern and prefixes `https://`; it trims surrounding punctuation and calls `redteam.ParseTarget`. It returns an error without invoking the service when no valid target exists.

Implement `Terminal.Run` with a line-reader goroutine and `select` over line input, context cancellation and the injected signal channel. During an active engagement, run `EngagementRunner.Run` in a goroutine; on `os.Interrupt`, call only that engagement cancel function, wait for its result/artifacts, print outcome and return to the prompt. Implement `/help`, `/status`, `/quit` and `/exit`; no user line is consumed as a new task while the active engagement runs.

- [ ] **Step 4: Replace CLI scan dispatch**

Refactor `cmd/pentgo` so:

```go
switch args[0] {
case "-h", "--help", "help":
	printHelp(stdout)
	return 0
case "-v", "--version", "version":
	fmt.Fprintln(stdout, "PentGo", version)
	return 0
default:
	fmt.Fprintf(stderr, "error: unknown command: %s\n", args[0])
	return 2
}
```

When `len(args)==0`, build `app.Service`, construct `terminal.Terminal` with `os.Stdin/os.Stdout`, and route `SIGINT` to the terminal instead of wrapping the full process in a single `signal.NotifyContext`. Keep `SIGTERM` as process-level termination. Update help text so it documents `pentgo`, natural language target input and built-in REPL commands, without documenting `scan`.

- [ ] **Step 5: Run terminal and command tests**

Run:

```bash
go test ./internal/terminal ./cmd/pentgo -count=1
```

Expected: PASS. Verify `pentgo --help` succeeds, `pentgo scan https://TARGET` returns exit 2, and a natural language fixture run produces an engagement directory.

- [ ] **Step 6: Commit the REPL task**

```bash
git add internal/terminal tests/_packages/internal/terminal cmd/pentgo/main.go tests/_packages/cmd/pentgo
git commit -m "feat: add natural language recon repl"
```

## Task 7: Update Documentation and Run Full Verification

**Files:**
- Modify: `README.md:1-123`

**Interfaces:**
- Documents: `pentgo` REPL invocation, natural language target requirement, `/help`/`/status`/`/quit`, agent config and artifacts.

- [ ] **Step 1: Update README examples**

Replace `pentgo scan` examples with:

```text
$ pentgo
pentgo> 对 https://TARGET 做资产侦察，优先关注子域名
```

Document both OpenAI and Anthropic config blocks, API Key environment variables, `recon.agent.enabled`, `max_turns`, and the behavior when Agent mode is disabled. State that the model selects registered Recon Skills and that each selected Skill writes a JSON evidence file.

- [ ] **Step 2: Format and run focused tests**

Run:

```bash
gofmt -w internal/config internal/agent internal/recon internal/redteam internal/report internal/app internal/terminal cmd/pentgo skills
go test ./internal/config ./internal/agent ./skills ./tests/packages/internal/recon ./internal/app ./internal/terminal ./cmd/pentgo -count=1
```

Expected: PASS.

- [ ] **Step 3: Run repository-wide verification**

Run:

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
go mod verify
go run ./cmd/pentgo --help
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 4: Commit documentation and final verification state**

```bash
git add README.md docs/superpowers/specs/2026-07-16-recon-agent-loop-design.md
git commit -m "docs: describe recon repl agent workflow"
```

## Plan Self-Review

- Spec coverage: Tasks 1-2 implement OpenAI/Anthropic configuration and native tool use; Tasks 3-4 implement registered Skill execution, evidence feedback, state changes and error behavior; Tasks 5-6 implement the REPL-only application boundary and cancellation; Task 7 updates docs and runs every required verification command.
- Placeholder scan: every task names concrete files, exported interfaces, test names, inputs, commands and expected outcomes.
- Type consistency: `agent.Client.Decide` produces `agent.Decision`; `recon.AgentRunner` consumes it; `app.Service` constructs the runner; `terminal.EngagementRunner` consumes the service; `cmd/pentgo` constructs the terminal.
