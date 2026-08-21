# Terminal Agent Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 engagement 终态后调用同一模型生成基于执行证据的中文 Markdown 报告，并在报告调用失败时发布确定性时间线报告。

**Architecture:** `runtime.Runner` 收集有界、无代码的 `ReportContext`。`report` 包使用独立 `agent.Client.Chat` 请求生成 Markdown，`EngagementWriter` 优先写模型输出，空输出、错误和取消时回退现有 Markdown 时间线。报告请求不进入 Agent Loop，不新增执行轮次。

**Tech Stack:** Go 标准库、现有 `internal/agent`、`internal/runtime`、Markdown。

## Global Constraints

- 不发送 native tools，不执行报告模型回复中的代码。
- 上下文不含模型代码、完整原始输出或 API 凭据。
- 取消、空文本或报告模型错误仍必须发布 `session.json`、`evidence/`、`work/` 和 `report.md`。
- 不添加第三方依赖；测试源码保留在 `tests/_packages/` 并通过符号链接暴露给生产包。

---

### Task 1: Runtime Report Context

**Files:**
- Create: `internal/runtime/report_context.go`
- Create: `tests/_packages/internal/runtime/report_context_test.go`
- Create: `internal/runtime/report_context_test.go` (符号链接)
- Modify: `internal/runtime/runner.go`
- Modify: `tests/_packages/internal/runtime/runner_test.go`

**Interfaces:**
- Produces: `ReportContext`、`ReportTurn`、`ReportBlock`、`func (r *Runner) ReportContext(*AgentSession) ReportContext`、`func (c ReportContext) PromptText() string`。
- Consumes: `ExecutionResult`、`AgentSession`、`assistantSummary`。

- [ ] **Step 1: Write the failing bounded-context test**

```go
func TestReportContextPromptExcludesCodeAndBoundsOutput(t *testing.T) {
    context := ReportContext{Target: "https://example.test", Turns: []ReportTurn{{
        Number: 1, Decision: "检查首页", Blocks: []ReportBlock{{
            Language: LanguagePython, Status: ExecutionSucceeded,
            Stdout: strings.Repeat("x", 5000),
            EvidencePath: "evidence/agent-turn-001-block-001.json",
        }},
    }}}
    prompt := context.PromptText()
    if len(prompt) > maxReportContextBytes || strings.Contains(prompt, "print(") || !strings.Contains(prompt, "evidence/agent-turn-001-block-001.json") {
        t.Fatalf("prompt = %q", prompt)
    }
}
```

- [ ] **Step 2: Verify the test fails**

Run: `go test ./internal/runtime -run TestReportContextPromptExcludesCodeAndBoundsOutput -count=1`

Expected: FAIL because `ReportContext` is undefined.

- [ ] **Step 3: Implement code-free report context**

```go
const maxReportContextBytes = 16 * 1024

type ReportBlock struct {
    Index int
    Language Language
    Status ExecutionStatus
    ExitCode int
    Stdout string
    Stderr string
    EvidencePath string
}

type ReportTurn struct {
    Number int
    Decision string
    Blocks []ReportBlock
}

type ReportContext struct {
    Target string
    Intent string
    Status SessionStatus
    StopReason string
    Skills []string
    Turns []ReportTurn
    RecoveryEvents []TimelineEvent
}
```

Have Runner record `assistantSummary`, each preflight result, and each execution result. Truncate stdout/stderr before storage. `PromptText` must append lines until `maxReportContextBytes`, then append `[additional execution summaries omitted]`.

- [ ] **Step 4: Add a Runner integration test**

```go
func TestRunnerExposesCodeFreeExecutionReportContext(t *testing.T) {
    // Run a scripted code response followed by TASK_COMPLETE.
    // Assert decision, status and evidence path exist; Python source does not.
}
```

- [ ] **Step 5: Verify Runtime tests pass**

Run: `go test ./internal/runtime -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/report_context.go internal/runtime/runner.go internal/runtime/report_context_test.go tests/_packages/internal/runtime/report_context_test.go tests/_packages/internal/runtime/runner_test.go
git commit -m "feat: collect bounded terminal report context"
```

### Task 2: Independent Report Generator and Writer Preference

**Files:**
- Create: `internal/report/generator.go`
- Create: `tests/_packages/internal/report/generator_test.go`
- Create: `internal/report/generator_test.go` (符号链接)
- Modify: `internal/report/artifacts.go`
- Modify: `tests/_packages/internal/report/artifacts_test.go`

**Interfaces:**
- Produces: `func GenerateTerminalMarkdown(context.Context, agent.Client, runtime.ReportContext) (string, error)`.
- Produces: `func (w *EngagementWriter) PublishWithReport(*runtime.AgentSession, time.Time, string) (Artifacts, error)`.

- [ ] **Step 1: Write the failing independent-request test**

```go
func TestGenerateTerminalMarkdownUsesIndependentEvidenceOnlyRequest(t *testing.T) {
    client := &recordingClient{response: agent.Response{Content: "# 报告\n\n## 已验证发现\n无。"}}
    markdown, err := GenerateTerminalMarkdown(context.Background(), client, runtime.ReportContext{Target: "https://example.test"})
    if err != nil || markdown == "" || len(client.requests) != 1 { t.Fatal(markdown, err) }
    request := client.requests[0]
    if strings.Contains(request.SystemPrompt, "execute") || strings.Contains(request.Messages[0].Content, "```python") { t.Fatal(request) }
}
```

- [ ] **Step 2: Verify the test fails**

Run: `go test ./internal/report -run TestGenerateTerminalMarkdownUsesIndependentEvidenceOnlyRequest -count=1`

Expected: FAIL because `GenerateTerminalMarkdown` is undefined.

- [ ] **Step 3: Implement generator and Markdown preference**

```go
const terminalReportSystemPrompt = `生成中文 Markdown 渗透测试报告。只使用提供的执行证据；不要运行代码，不要编造漏洞。`

func GenerateTerminalMarkdown(ctx context.Context, client agent.Client, context runtime.ReportContext) (string, error) {
    if client == nil { return "", errors.New("nil report client") }
    response, err := client.Chat(ctx, agent.Request{
        SystemPrompt: terminalReportSystemPrompt,
        Messages: []agent.Message{{Role: "user", Content: context.PromptText()}},
    })
    if err != nil { return "", err }
    if markdown := strings.TrimSpace(response.Content); markdown != "" { return markdown, nil }
    return "", errors.New("report model returned empty content")
}
```

The system prompt must require `目标与范围`、`执行摘要`、`已验证发现`、`证据索引`、`影响与修复建议`、`未完成或受阻项目`; no execution evidence means `未验证漏洞`. `PublishWithReport` writes valid nonempty Markdown verbatim, otherwise calls `renderMarkdown`.

- [ ] **Step 4: Add writer tests**

```go
func TestPublishWithReportWritesModelMarkdown(t *testing.T) {
    artifacts, _ := writer.PublishWithReport(session, now, "# 模型报告\n")
    body, _ := os.ReadFile(artifacts.Markdown)
    if string(body) != "# 模型报告\n" { t.Fatal(string(body)) }
}

func TestPublishWithReportFallsBackForEmptyMarkdown(t *testing.T) {
    artifacts, _ := writer.PublishWithReport(session, now, "  ")
    body, _ := os.ReadFile(artifacts.Markdown)
    if !strings.Contains(string(body), "## Execution Timeline") { t.Fatal(string(body)) }
}
```

- [ ] **Step 5: Verify Report tests pass**

Run: `go test ./internal/report -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/report/generator.go internal/report/artifacts.go internal/report/generator_test.go tests/_packages/internal/report/generator_test.go tests/_packages/internal/report/artifacts_test.go
git commit -m "feat: generate terminal agent reports"
```

### Task 3: App Orchestration, Events, and Fallback

**Files:**
- Modify: `internal/app/engagement.go`
- Modify: `tests/_packages/internal/app/engagement_test.go`
- Modify: `internal/terminal/terminal.go`
- Modify: `tests/_packages/internal/terminal/terminal_test.go`

**Interfaces:**
- Consumes: `Runner.ReportContext`、`GenerateTerminalMarkdown`、`PublishWithReport`。
- Produces: progress text `Generating final report.`, `Final report generated.`, `Final report fell back to execution timeline.`.

- [ ] **Step 1: Write the failing App success-path test**

```go
func TestServiceUsesThirdModelRequestForFinalReport(t *testing.T) {
    client := &scriptedClient{responses: []agent.Response{
        {Content: "```python\nimport os\nprint('evidence')\n```"},
        {Content: "TASK_COMPLETE"},
        {Content: "# 最终报告\n\n## 已验证发现\n无。"},
    }}
    result, err := service.Run(context.Background(), request, collectEvents)
    body, _ := os.ReadFile(result.Artifacts.Markdown)
    if err != nil || len(client.requests) != 3 || !strings.HasPrefix(string(body), "# 最终报告") { t.Fatal(result, err) }
}
```

- [ ] **Step 2: Verify the test fails**

Run: `go test ./internal/app -run TestServiceUsesThirdModelRequestForFinalReport -count=1`

Expected: FAIL because App only calls Runner and deterministic `Publish`.

- [ ] **Step 3: Generate report after Runner terminal state**

```go
reportMarkdown := ""
if ctx.Err() == nil {
    progress(Event{Message: "Generating final report."})
    markdown, reportErr := report.GenerateTerminalMarkdown(ctx, client, runner.ReportContext(session))
    if reportErr == nil {
        reportMarkdown = markdown
        progress(Event{Message: "Final report generated."})
    } else {
        progress(Event{Message: "Final report fell back to execution timeline."})
    }
} else {
    progress(Event{Message: "Final report fell back to execution timeline."})
}
artifacts, publishErr := writer.PublishWithReport(session, service.now(), reportMarkdown)
```

Do not put report errors in `result.RunError`; preserve Runner outcome. Do not show model report body or code in terminal progress.

- [ ] **Step 4: Add fallback and cancellation tests**

```go
func TestServicePublishesTimelineWhenReportCallFails(t *testing.T) {
    // Third Chat call returns an error.
    // Assert report.md contains "## Execution Timeline" and fallback event exists.
}

func TestServiceSkipsReportCallForCancelledContext(t *testing.T) {
    // Cancel after Runner reaches terminal state.
    // Assert no third request occurs and artifacts exist.
}
```

- [ ] **Step 5: Verify App and Terminal tests pass**

Run: `go test ./internal/app ./internal/terminal -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/engagement.go internal/terminal/terminal.go tests/_packages/internal/app/engagement_test.go tests/_packages/internal/terminal/terminal_test.go
git commit -m "feat: publish model-generated terminal reports"
```

### Task 4: Documentation and Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-07-17-terminal-agent-report-design.md`

- [ ] **Step 1: Document terminal report generation**

State that `report.md` is a report-only model call over bounded execution summaries and evidence paths; state that model error or cancellation produces a deterministic timeline fallback; retain `session.json`, `evidence/`, and `work/` documentation.

- [ ] **Step 2: Mark design implemented**

Change `**状态：** 待评审` to `**状态：** 已实现`.

- [ ] **Step 3: Run final verification**

Run: `gofmt -w $(rg --files internal/runtime internal/report internal/app internal/terminal tests/_packages -g '*.go')`

Run: `go test ./... -count=1`

Run: `go test -race ./... -count=1`

Run: `go vet ./...`

Run: `go build ./...`

Run: `go mod verify`

Run: `git diff --check`

Expected: every command exits with code 0; `go mod verify` prints `all modules verified`.

- [ ] **Step 4: Commit documentation**

```bash
git add README.md docs/superpowers/specs/2026-07-17-terminal-agent-report-design.md
git commit -m "docs: describe terminal agent report generation"
```
