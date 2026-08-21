# Remove Nuclei Bingo Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完全移除 nuclei，将 Scan 固定为 Bingo 式的 SQLi 后 XSS 初筛调度。

**Architecture:** Pipeline 继续在 Recon 后构造受限候选并调用独立 Scan Runner。Runner 不再拥有本地执行器，仅以一个受超时限制的 HTTP client 依次批量运行 SQLi 与 XSS；每个候选失败独立落盘并继续，阶段仅在取消、evidence 写入失败或基础设施错误时结束。

**Tech Stack:** Go 1.25、标准库 `net/http`/`net/url`、现有 `report.EngagementWriter`。

## Global Constraints

- Scan 顺序固定为 `SQLi -> XSS`，阶段顺序固定为 `recon -> scan -> verify -> persist`。
- 保留 canonical target、同源 sitemap 候选、稳定去重与 20 URL 上限。
- 保留 SQLi“新增数据库错误”与 XSS“2xx 原样反射唯一探针”的判定。
- 不保留 nuclei 配置、命令、模板、evidence、JSONL 解析、`skill.Executor` 或任何 Scan 本地命令依赖。
- 不加入 LFI、SSRF、IDOR、利用、数据提取、Scan Agent 或 Tscan 自由文本 URL 解析。
- evidence 不保存 query value、userinfo 或原始 HTTP 响应正文。
- 不暂存工作区已有的 `README.md`、Pipeline、Session、应用服务及其现有测试改动。

---

### Task 1: 删除 nuclei 配置与 Scan 类型

**Files:**
- Modify: `internal/config/config.go`
- Modify: `tests/_packages/internal/config/config_test.go`
- Modify: `internal/redteam/phases/scan/types.go`
- Modify: `internal/redteam/phases/scan/runner.go`
- Modify: `internal/app/engagement.go`
- Modify: `tests/_packages/internal/app/engagement_test.go`
- Delete: `internal/redteam/phases/scan/nuclei.go`
- Delete: `tests/packages/internal/redteam/phases/scan/nuclei.go`

**Produces:** 只含 HTTP probe 的 `ScanConfig` 与无本地命令依赖的 Scan 数据模型。

- [ ] **Step 1: 写入失败测试**

在 `config_test.go` 将默认 Scan 断言替换为：

```go
scan := Default().Scan
if !scan.Enabled || scan.TimeoutSeconds != 900 || scan.MaxURLs != 20 {
	t.Fatalf("Scan default = %+v", scan)
}
if !scan.HTTPProbe.Enabled || scan.HTTPProbe.TimeoutSeconds != 15 || scan.HTTPProbe.MaxParametersPerURL != 10 {
	t.Fatalf("HTTP probe default = %+v", scan.HTTPProbe)
}
data, _ := json.Marshal(Default())
if strings.Contains(string(data), `"nuclei"`) {
	t.Fatalf("default config contains nuclei: %s", data)
}
```

将局部配置输入替换为 `{"scan":{"enabled":false,"http_probe":{"max_parameters_per_url":2}}}`，断言 `Scan.Enabled == false`、HTTP 参数上限为 2，且缺失数值均由默认值补齐。

在 `scan_test.go` 删除所有 nuclei、`recordingExecutor` 和 `skill` import；保留 `Result` 独立性测试，新增：

```go
func TestScanTypesHaveNoCommandResult(t *testing.T) {
	_ = HTTPResponseSummary{StatusCode: http.StatusOK, BodyBytes: 12}
}
```

新增 `TestScanRunnerRunsHTTPOnlyFlow`：使用带 query 的 `httptest.Server` 与 `NewRunner(config.Default().Scan, server.Client(), sink, nil, progress)`，断言 progress 只为 `Scan [SQLI]`、`Scan [XSS]`，不需传入 executor。在 `engagement_test.go` 新增 `TestServiceBuildsHTTPOnlyScanRunner`：

```go
runner := NewService(config.Default(), Dependencies{}).newScanRunner(nil, nil)
if runner == nil {
	t.Fatal("newScanRunner returned nil")
}
```

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/config ./internal/redteam/phases/scan ./internal/app -run 'TestDefaultIncludesScanConfiguration|TestLoadMergesScanConfiguration|TestScanTypesHaveNoCommandResult|TestScanRunnerRunsHTTPOnlyFlow|TestServiceBuildsHTTPOnlyScanRunner' -count=1
```

Expected: FAIL，因为旧 `NucleiConfig` 仍存在，配置 JSON 仍包含 `nuclei`，`NewRunner` 仍需要 executor。

- [ ] **Step 3: 实现最小删除**

将配置模型收敛为：

```go
type ScanConfig struct {
	Enabled        bool            `json:"enabled"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	MaxURLs        int             `json:"max_urls"`
	HTTPProbe      HTTPProbeConfig `json:"http_probe"`
}
```

删除 `NucleiConfig`、`NucleiEvidence`、`skill` import、`defaultScanConfig` 中 nuclei 块，以及 `normalizeScanConfig` 的 nuclei 分支；默认保留 `enabled=true`、900 秒、20 URL 和 HTTP probe 15 秒/10 参数。使用 `apply_patch` 删除 `nuclei.go` 与其测试镜像链接。

同步将 `Runner` 移除 executor 字段和构造参数，将 `durationSeconds` 放入 `runner.go`，并以 SQLi 后 XSS 替换旧 nuclei 调用。更新 `Service.newScanRunner`：

```go
return scan.NewRunner(s.cfg.Scan, client, writer, s.deps.Clock, progress)
```

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/config/config.go internal/redteam/phases/scan/types.go internal/redteam/phases/scan/runner.go internal/app/engagement.go tests/_packages/internal/config/config_test.go tests/_packages/internal/app/engagement_test.go tests/packages/internal/redteam/phases/scan/scan_test.go
go test ./internal/config ./internal/redteam/phases/scan ./internal/app -run 'TestDefaultIncludesScanConfiguration|TestLoadMergesScanConfiguration|TestScanTypesHaveNoCommandResult|TestScanRunnerRunsHTTPOnlyFlow|TestServiceBuildsHTTPOnlyScanRunner' -count=1
git diff --check
git status --short
```

Expected: 聚焦测试 PASS，且不暂存已有未提交的应用层改动。

### Task 2: SQLi 后 XSS 的 Bingo 式 Runner 调度

**Files:**
- Modify: `internal/redteam/phases/scan/runner.go`
- Modify: `internal/redteam/phases/scan/http_probe.go`
- Modify: `tests/packages/internal/redteam/phases/scan/scan_test.go`

**Consumes:** Task 1 的 HTTP-only `ScanConfig`、现有 `runSQLi` 和 `runXSS`。

**Produces:** 无本地执行器、按 SQLi 后 XSS 固定调度的 `scan.Runner`。

**Interfaces:**

```go
type Runner struct {
	config config.ScanConfig
	client *http.Client
	sink EvidenceSink
	clock func() time.Time
	progress func(string)
}

func NewRunner(config.ScanConfig, *http.Client, EvidenceSink, func() time.Time, func(string)) *Runner
func (r *Runner) Run(context.Context, Input) (Result, error)
```

- [ ] **Step 1: 写入失败测试**

新增 `TestScanRunnerRunsSQLiBeforeXSS`。`httptest.Server` 对单引号参数返回 SQL syntax 文本、对 XSS 参数原样回显；调用：

```go
progress := []string{}
runner := NewRunner(config.Default().Scan, server.Client(), sink, nil, func(message string) {
	progress = append(progress, message)
})
result, err := runner.Run(context.Background(), Input{
	CanonicalTarget: server.URL,
	Candidates: []string{server.URL + "?id=1&q=hello"},
})
```

断言 `err == nil`、`progress == []string{"Scan [SQLI]", "Scan [XSS]"}`、`result.Calls` 的 Kind 顺序为 `sqli`、`xss`，evidence 名称顺序为 `scan-sqli-001`、`scan-xss-001`，并且 Result 同时包含 `possible_sqli` 与 `possible_reflected_xss`。

将所有现有 `newHTTPProbeRunner` 调用更新为新构造函数。新增 `TestScanRunnerContinuesWithXSSAfterSQLiTransportFailure`：第一候选使用关闭 server、第二候选回显 XSS 探针；断言 SQLi 有 failed Call，XSS 仍处理第二候选并产生 Finding。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/redteam/phases/scan -run 'TestScanRunnerRunsSQLiBeforeXSS|TestScanRunnerContinuesWithXSSAfterSQLiTransportFailure' -count=1
```

Expected: FAIL，如果 Task 1 只完成了构造签名收缩而未保留全部候选的 SQLi 后 XSS 调度，此测试将在失败候选之后缺失 XSS Finding。

- [ ] **Step 3: 实现固定 HTTP 调度**

从 `Runner`、`NewRunner` 与调用方移除 `skill.Executor` 参数和字段。将 `durationSeconds` 从删除的 nuclei 文件移动到 `runner.go`，使 nil HTTP client 使用：

```go
&http.Client{Timeout: durationSeconds(configuration.HTTPProbe.TimeoutSeconds)}
```

`Run` 保留 nil、配置、sink、canonical target 和总超时检查，随后只执行：

```go
r.progress("Scan [SQLI]")
sqliCalls, sqliFindings, err := r.runSQLi(ctx, input.Candidates)
if err != nil {
	return Result{Calls: sqliCalls, Findings: sqliFindings}, err
}

r.progress("Scan [XSS]")
xssCalls, xssFindings, err := r.runXSS(ctx, input.Candidates)
result := Result{
	Calls: append(sqliCalls, xssCalls...),
	Findings: append(sqliFindings, xssFindings...),
}
```

在 SQLi 初筛中保持单候选 `failed` Call 不返回 error；在 XSS 阶段仍遍历全部候选，因此 SQLi 传输失败不会跳过 XSS。仅将 context 取消与 evidence sink 错误返回为 `err`。

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/redteam/phases/scan/runner.go internal/redteam/phases/scan/http_probe.go tests/packages/internal/redteam/phases/scan/scan_test.go
go test ./internal/redteam/phases/scan -count=1
git add internal/redteam/phases/scan/runner.go internal/redteam/phases/scan/http_probe.go tests/packages/internal/redteam/phases/scan/scan_test.go
git commit -m "refactor: run Bingo-style SQLi and XSS scan"
```

Expected: Scan 包测试 PASS，Runner 没有 `skill` 或 nuclei 依赖。

### Task 3: 应用组装、文档和授权目标回归

**Files:**
- Modify: `internal/app/engagement.go`
- Modify: `tests/_packages/internal/app/engagement_test.go`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-07-16-scan-baseline-design.md`

**Consumes:** Task 2 的新 `scan.NewRunner` 签名与 HTTP-only `ScanConfig`。

**Produces:** 应用层不再为 Scan 创建本地执行器，用户文档和设计准确描述 SQLi/XSS 流程。

- [ ] **Step 1: 写入失败测试**

在 `engagement_test.go` 的 `TestServiceCompletesDisabledScanPhase` 之外新增 `TestServiceBuildsHTTPOnlyScanRunner`：

```go
runner := NewService(config.Default(), Dependencies{}).newScanRunner(nil, nil)
if runner == nil {
	t.Fatal("newScanRunner returned nil")
}
```

该测试在 Task 2 改变构造签名后确保应用层能编译并创建 HTTP-only Runner；保留禁用 Scan 后 Session phase 完成的断言。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/app -run 'TestServiceBuildsHTTPOnlyScanRunner|TestServiceCompletesDisabledScanPhase' -count=1
```

Expected: FAIL，因为 `newScanRunner` 仍向 `scan.NewRunner` 传入 `skill.LocalExecutor{}`。

- [ ] **Step 3: 实现应用与文档更新**

在 `newScanRunner` 改用：

```go
return scan.NewRunner(s.cfg.Scan, client, writer, s.deps.Clock, progress)
```

保留 `skill` import，因为 Recon runner 仍需要 `skill.LocalExecutor{}`。README 流程改为 `Scan(SQLi -> XSS initial probes)`，Scan JSON 删除 `nuclei` 对象。旧 `scan-baseline-design.md` 的状态改为“已被 `scan-sqli-xss-bingo-flow-design.md` 取代”，不重写历史设计正文。

- [ ] **Step 4: 验证、隔离暂存并执行授权回归**

Run:

```bash
gofmt -w internal/app/engagement.go tests/_packages/internal/app/engagement_test.go
go test ./internal/app ./internal/redteam ./internal/report -count=1
go test ./...
go test -race ./...
go vet ./...
go build ./...
go mod verify
rg -n 'nuclei|scan-nuclei|skill.Executor' internal/config internal/redteam/phases/scan README.md
```

使用现有 `/tmp/pentgo-scan-test/pentgo/config.json`，在其 `recon.agent.enabled=false`、FOFA、Shodan、subfinder、Tscan 关闭的配置下启动 REPL，并输入：

```text
对 https://lycvc.linyi.cn/ 执行资产侦察和自动扫描
```

断言终端依次显示 `Phase [RECON] started`、`Phase [SCAN] started`、`Scan [SQLI]`、`Scan [XSS]`，生成的 `session.json` 中 `scan` 为 `done` 且不存在 `scan-nuclei.json`。

不暂存 `internal/app/engagement.go`、README 与既有未提交测试文件；仅当这些文件的既有改动已单独处理后，再用精确 hunk 提交应用与文档更新。

Expected: 授权目标完整运行至 Scan、Verify 和 Persist，且无 nuclei 模板或本地命令依赖。`rg` 不输出任何生产匹配。
