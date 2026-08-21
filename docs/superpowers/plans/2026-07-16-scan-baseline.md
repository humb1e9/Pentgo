# Scan 基线阶段实现计划

**状态：** 已被 `2026-07-16-remove-nuclei-bingo-scan.md` 取代。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Recon 后自动执行 nuclei、基于 Recon URL 的 SQLi 初筛和反射型 XSS 初筛，并发布 evidence、Session Finding 和 Markdown 报告。

**Architecture:** `internal/redteam` 从已完成的 Recon snapshot 提取已授权 URL，并按 `recon -> scan -> verify -> persist` 编排。`internal/redteam/phases/scan` 只消费 Scan 输入、配置、执行器、HTTP client 和 evidence sink，返回 phase-local `Result` 与 `scan.Finding`；Pipeline 将 Finding 映射为 `redteam.Finding`。

**Tech Stack:** Go 1.25、标准库 `net/http`、`net/url`、`encoding/json`、现有 `skill.Executor`、`report.EngagementWriter`。

## 全局约束

- 只实现 nuclei、SQLi 初筛、反射型 XSS 初筛；不加入 nmap、Exploit、Scan Agent 或 Tscan 自由文本 URL 解析。
- 候选始终含 canonical target，附加同源 sitemap URL，稳定去重后最多 20 条。
- nuclei 仅扫描 canonical target，参数固定为 `-u TARGET -severity critical,high,medium -jsonl -silent`。
- SQLi 仅在单引号探针新增数据库错误特征时报告；XSS 仅在 2xx 响应原样反射完整唯一 HTML 探针时报告。
- 每项执行写 `scan-*` evidence；`skipped` 是可审计终态。
- 保留用户未提交改动，特别是 `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md`。

---

### Task 1: 配置、Scan 契约与候选提取

**Files:**
- Modify: `internal/config/config.go`
- Modify: `tests/_packages/internal/config/config_test.go`
- Create: `internal/redteam/phases/scan/types.go`
- Create: `internal/redteam/phases/scan/evidence.go`
- Create: `internal/redteam/phases/scan/scan_test.go` symbolic link to the test mirror
- Create: `internal/redteam/scan_candidates.go`
- Create: `tests/_packages/internal/redteam/scan_candidates_test.go`
- Create: `internal/redteam/scan_candidates_test.go` symbolic link to the test mirror
- Create: `tests/packages/internal/redteam/phases/scan/types.go` and `evidence.go` symbolic links
- Create: `tests/packages/internal/redteam/phases/scan/scan_test.go`

**Interfaces:**

```go
type ScanConfig struct {
	Enabled        bool               `json:"enabled"`
	TimeoutSeconds int                `json:"timeout_seconds"`
	MaxURLs        int                `json:"max_urls"`
	Nuclei         NucleiConfig       `json:"nuclei"`
	HTTPProbe      HTTPProbeConfig    `json:"http_probe"`
}
type NucleiConfig struct {
	Enabled        bool   `json:"enabled"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxFindings    int    `json:"max_findings"`
}
type HTTPProbeConfig struct {
	Enabled             bool `json:"enabled"`
	TimeoutSeconds      int  `json:"timeout_seconds"`
	MaxParametersPerURL int  `json:"max_parameters_per_url"`
}

type EvidenceSink interface { WriteEvidence(string, any) (string, error) }
type Status string
type Finding struct { Title, Severity, Description, Evidence string; Metadata map[string]string }
type Input struct { CanonicalTarget string; Candidates []string }
type Call struct { Kind, Target string; Status Status; Summary, Error, EvidencePath string }
type Result struct { Summary string; Calls []Call; Findings []Finding }

func scanCandidates(Target, AuthorizationContext, recon.Snapshot, int) []string
```

- [ ] **Step 1: 写入失败测试**

在 `config_test.go` 增加 `TestDefaultIncludesScanConfiguration`，断言 `Default().Scan` 为 enabled、900 秒、20 URL、nuclei `nuclei`/600 秒/30 findings、HTTP probe 15 秒/10 参数。

增加 `TestLoadMergesScanConfiguration`：写入 `{"scan":{"enabled":false,"nuclei":{"enabled":false,"max_findings":5},"http_probe":{"max_parameters_per_url":2}}}`，断言显式的 Scan/Nuclei enabled false 保留不变，`MaxFindings == 5`、`MaxParametersPerURL == 2`，未提供的 Scan 总超时、URL 上限、nuclei 命令/超时和 HTTP 超时均由默认值补齐。

在 `scan_candidates_test.go` 构造 `http_sitemap` observation，包含 canonical URL、重复 URL、异源 URL 和 21 个同源 URL。断言只返回同源 URL、canonical 位于首位、顺序稳定、无重复且总数为 20；缺少 sitemap 时只返回 canonical URL。

在 `scan_test.go` 断言 `Result{Summary: "done", Calls: []Call{}, Findings: []Finding{}}` 可直接构造，且 `Finding` 不依赖 `redteam` 包。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/config ./internal/redteam ./internal/redteam/phases/scan -run 'TestDefaultIncludesScanConfiguration|TestLoadMergesScanConfiguration|TestScanCandidates|TestScanResult' -count=1
```

Expected: FAIL，因为 Scan 配置、候选函数和 Scan 类型尚不存在。

- [ ] **Step 3: 实现最小代码**

在 `Config` 加入 `Scan ScanConfig`。默认值为 `enabled=true`、`timeout_seconds=900`、`max_urls=20`、nuclei `enabled=true`/`command="nuclei"`/`timeout_seconds=600`/`max_findings=30`、HTTP probe `enabled=true`/`timeout_seconds=15`/`max_parameters_per_url=10`。新增 `defaultScanConfig` 与 `normalizeScanConfig`，归一化只补齐非正数和空命令，保留 JSON 的显式 `enabled:false`；`Default` 调用 `defaultScanConfig()`，`Load` 在 `normalizeReconConfig(&cfg.Recon)` 后调用 `normalizeScanConfig(&cfg.Scan)`。

`scanCandidates` 从 `http_sitemap.attributes["urls"]` 拆分 URL，使用 `url.Parse` 和 `AuthorizationContext.Allows` 校验，按 URL 字符串去重和排序，canonical target 始终排在第一位，`max <= 0` 使用 20。

按现有测试镜像模式创建连接，测试正文仍只位于 `tests` 目录：

```bash
mkdir -p tests/packages/internal/redteam/phases/scan
ln -s ../../tests/_packages/internal/redteam/scan_candidates_test.go internal/redteam/scan_candidates_test.go
ln -s ../../../../tests/packages/internal/redteam/phases/scan/scan_test.go internal/redteam/phases/scan/scan_test.go
ln -s ../../../../../../internal/redteam/phases/scan/types.go tests/packages/internal/redteam/phases/scan/types.go
ln -s ../../../../../../internal/redteam/phases/scan/evidence.go tests/packages/internal/redteam/phases/scan/evidence.go
```

在 Scan 包定义 `succeeded`、`skipped`、`failed`、`cancelled` 状态、数据类型和 `NucleiEvidence`、`SQLiEvidence`、`XSSEvidence`。每份 evidence 都保存 schema version、调用摘要、错误、扫描明细和 Finding 切片。

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/config/config.go internal/redteam/scan_candidates.go internal/redteam/phases/scan/types.go internal/redteam/phases/scan/evidence.go tests/_packages/internal/config/config_test.go tests/_packages/internal/redteam/scan_candidates_test.go tests/packages/internal/redteam/phases/scan/scan_test.go
go test ./internal/config ./internal/redteam ./internal/redteam/phases/scan -run 'TestDefaultIncludesScanConfiguration|TestLoadMergesScanConfiguration|TestScanCandidates|TestScanResult' -count=1
git add internal/config/config.go internal/redteam/scan_candidates.go internal/redteam/phases/scan tests/_packages/internal/config/config_test.go tests/_packages/internal/redteam/scan_candidates_test.go tests/packages/internal/redteam/phases/scan
git commit -m "feat: define scan configuration and candidates"
```

Expected: focused tests PASS.

### Task 2: nuclei 固定扫描

**Files:**
- Create: `internal/redteam/phases/scan/runner.go`
- Create: `internal/redteam/phases/scan/nuclei.go`
- Create: `tests/packages/internal/redteam/phases/scan/runner.go` and `nuclei.go` symbolic links
- Modify: `tests/packages/internal/redteam/phases/scan/scan_test.go`

**Interfaces:**

```go
type Runner struct {
	config config.ScanConfig
	executor skill.Executor
	client *http.Client
	sink EvidenceSink
	clock func() time.Time
	progress func(string)
}
func NewRunner(config.ScanConfig, skill.Executor, *http.Client, EvidenceSink, func() time.Time, func(string)) *Runner
func (r *Runner) Run(context.Context, Input) (Result, error)
func (r *Runner) runNuclei(context.Context, string) (Call, []Finding, error)
```

- [ ] **Step 1: 写入失败测试**

使用记录型 `skill.Executor` 返回两行 nuclei JSONL：一条 `high`、一条 `critical`。`TestScanRunnerRunsNuclei` 断言命令严格等于：

```go
[]string{os.Args[0], "-u", "https://example.test", "-severity", "critical,high,medium", "-jsonl", "-silent"}
```

断言两个 Finding 的 severity 为 `high`/`critical`，evidence 路径为 `evidence/scan-nuclei.json`。用 `MaxFindings: 1` 断言只保留第一条。添加 command 缺失和 nuclei 禁用用例，均断言写入 `scan-nuclei` evidence 且 status 为 `skipped`。添加 Scan 禁用用例，断言结果摘要为 `Scan is disabled by configuration.`、executor 调用次数为 0，且不发起 HTTP 请求。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/redteam/phases/scan -run 'TestScanRunnerRunsNuclei|TestScanRunnerSkipsNuclei' -count=1
```

Expected: FAIL，因为 `NewRunner` 与 `runNuclei` 尚不存在。

- [ ] **Step 3: 实现 nuclei**

`NewRunner` 为 nil executor 设置 `skill.LocalExecutor{}`，为 nil HTTP client 设置 `http.DefaultClient`，为 nil clock 设置 `time.Now().UTC`，为 nil progress 设置空函数。`Run` 在 `TimeoutSeconds > 0` 时使用 `context.WithTimeout(ctx, time.Duration(r.config.TimeoutSeconds)*time.Second)` 包住 nuclei 和全部 HTTP 初筛；关闭 Scan 时返回 `Result{Summary: "Scan is disabled by configuration."}`，不执行本地命令或 HTTP 请求。`runNuclei` 检查 Nuclei 和 command 开关，使用 `exec.LookPath`；每个跳过分支调用 `sink.WriteEvidence("scan-nuclei", NucleiEvidence{...})`。

创建源码镜像连接，使此包的测试运行时能编译最新实现：

```bash
ln -s ../../../../../../internal/redteam/phases/scan/runner.go tests/packages/internal/redteam/phases/scan/runner.go
ln -s ../../../../../../internal/redteam/phases/scan/nuclei.go tests/packages/internal/redteam/phases/scan/nuclei.go
```

成功路径用 `skill.Command` 运行固定参数。将 `skill.Result` 映射为 Scan status。逐行 `json.Unmarshal` nuclei JSONL，跳过空行和格式错误行，最多接受 `MaxFindings` 个有效对象。Finding metadata 写入 `scan_kind=nuclei`、`target`、`template_id`、`matched_at`、`matcher_type`，完整 `skill.Result` 和 findings 写入 `NucleiEvidence`。

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/redteam/phases/scan/runner.go internal/redteam/phases/scan/nuclei.go tests/packages/internal/redteam/phases/scan/scan_test.go
go test ./internal/redteam/phases/scan -run 'TestScanRunnerRunsNuclei|TestScanRunnerSkipsNuclei' -count=1
git add internal/redteam/phases/scan tests/packages/internal/redteam/phases/scan
git commit -m "feat: add fixed nuclei scan"
```

Expected: focused tests PASS.

### Task 3: SQLi 与反射型 XSS 初筛

**Files:**
- Create: `internal/redteam/phases/scan/http_probe.go`
- Create: `tests/packages/internal/redteam/phases/scan/http_probe.go` symbolic link
- Modify: `internal/redteam/phases/scan/runner.go`
- Modify: `tests/packages/internal/redteam/phases/scan/scan_test.go`

**Interfaces:**

```go
func (r *Runner) runSQLi(context.Context, []string) ([]Call, []Finding, error)
func (r *Runner) runXSS(context.Context, []string) ([]Call, []Finding, error)
func databaseErrorSignature(string) string
func uniqueXSSProbe(target, parameter string) string
```

- [ ] **Step 1: 写入失败测试**

通过 `httptest.Server` 增加：

```go
func TestScanRunnerReportsSQLiOnlyForNewDatabaseError(t *testing.T)
func TestScanRunnerReportsXSSOnlyForRawTwoXXReflection(t *testing.T)
func TestScanRunnerSkipsURLsWithoutQueryParameters(t *testing.T)
func TestScanRunnerContinuesAfterOneCandidateTransportFailure(t *testing.T)
```

SQLi 测试让 `id=1` 返回普通正文、`id='` 返回 `You have an error in your SQL syntax`，断言一个 severity `medium` 的 `possible_sqli`，metadata 参数为 `id`；基线和探针同时出现同一错误时断言无 Finding。

XSS 测试仅在 200 时原样返回完整探针，断言一个 `possible_reflected_xss`；改为 500 或 HTML 转义探针时断言无 Finding。无 query URL 断言 SQLi/XSS 各写一个 skipped evidence。第一个候选使用关闭的 server、第二个候选命中 SQLi 时断言第二个仍执行。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/redteam/phases/scan -run 'TestScanRunnerReportsSQLi|TestScanRunnerReportsXSS|TestScanRunnerSkipsURLsWithoutQueryParameters|TestScanRunnerContinuesAfterOneCandidateTransportFailure' -count=1
```

Expected: FAIL，因为 HTTP 初筛方法尚不存在。

- [ ] **Step 3: 实现 HTTP 初筛**

每条候选 URL 解析 query keys 并按字典序选取最多 `MaxParametersPerURL` 个。每个请求使用 `http.NewRequestWithContext`，客户端由 `HTTPProbe.TimeoutSeconds` 控制，正文由 `io.LimitReader` 限制为 1 MiB。

SQLi 探针只替换当前 key 为原值加单引号；`databaseErrorSignature` 依次匹配 `sql syntax`、`mysql`、`postgresql`、`sqlite`、`ora-`、`microsoft sql`、`odbc`。探针命中且基线未命中才生成 Finding。

XSS 探针格式为 `<script>pentgo-xss-<12 位 sha256></script>`，输入是候选 URL 与参数名；只在 200 至 299 状态和正文包含完整探针时生成 Finding。每个 URL 分别写 `scan-sqli-%03d`、`scan-xss-%03d` evidence。候选传输错误写 failed evidence 后继续；sink 错误或 context 取消直接返回。

创建 HTTP 初筛的源码镜像连接：

```bash
ln -s ../../../../../../internal/redteam/phases/scan/http_probe.go tests/packages/internal/redteam/phases/scan/http_probe.go
```

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/redteam/phases/scan/http_probe.go internal/redteam/phases/scan/runner.go tests/packages/internal/redteam/phases/scan/scan_test.go
go test ./internal/redteam/phases/scan -count=1
git add internal/redteam/phases/scan tests/packages/internal/redteam/phases/scan
git commit -m "feat: add scan SQLi and XSS probes"
```

Expected: Scan 包测试 PASS.

### Task 4: 接入 Session、Pipeline、应用服务与报告

**Files:**
- Modify: `internal/redteam/session.go`
- Modify: `internal/redteam/recon_pipeline.go`
- Modify: `internal/app/engagement.go`
- Modify: `tests/_packages/internal/redteam/session_state_test.go`
- Modify: `tests/_packages/internal/redteam/recon_pipeline_test.go`
- Modify: `tests/_packages/internal/app/engagement_test.go`
- Modify: `tests/_packages/internal/report/artifacts_test.go`
- Modify: `README.md`

**Consumes:** `config.ScanConfig`、`scan.Runner`、`scan.Input`、`scan.Result`、`scan.Finding`、`scanCandidates`。

**Produces:** Scan 阶段的已完成 Session、结构化 Scan Finding 投影和可用的应用组装。

**Interfaces:**

```go
type ScanRunner interface {
	Run(context.Context, scan.Input) (scan.Result, error)
}

func NewReconPipeline(*recon.Runner, ScanRunner, func() time.Time, func(string)) *ReconPipeline
func (p *ReconPipeline) runScan(context.Context, *Session) error
func scanFinding(int, scan.Finding, time.Time) Finding
func (s *Service) newScanRunner(*report.EngagementWriter) *scan.Runner
```

- [ ] **Step 1: 写入失败测试**

在 `session_state_test.go` 的新会话断言中将有序阶段变更为：

```go
want := []string{"recon", "scan", "verify", "persist"}
if !reflect.DeepEqual(sess.PhaseOrder, want) {
	t.Fatalf("PhaseOrder = %v, want %v", sess.PhaseOrder, want)
}
if sess.Phases["scan"].Status != StatusPending {
	t.Fatalf("scan phase = %+v", sess.Phases["scan"])
}
```

在 `recon_pipeline_test.go` 新增 `pipelineScanRunner`，它记录 `scan.Input` 并返回一个 `possible_sqli` Finding。`TestReconPipelineRunsScanBeforeVerify` 断言 Scan runner 只被调用一次，其 `CanonicalTarget` 为 `https://example.test/`，Scan phase 为 done，Scan Finding 出现在 `session.Findings` 和 `session.Phases["scan"].Findings`，且 `verify` phase 在 Scan 之后完成。断言投影 Finding 为 `finding-001`、`SeverityMedium`、`Metadata["scan_kind"] == "sqli"`。

新增 `TestReconPipelineCancelsScanWhenRunnerReturnsContextCancellation`：fake Scan runner 返回 `context.Canceled`，断言 `Run` 返回同一错误、Session 与 Scan phase 均为 cancelled、`verify` 保持 pending。新增 `TestReconPipelineFailsScanWhenRunnerReturnsError`：fake Scan runner 返回 `errors.New("scan fixture failed")`，断言 Scan phase 为 failed、Session 为 failed、`verify` 保持 pending。

在 `engagement_test.go` 用 `config.Default()` 关闭 `cfg.Scan.Enabled`、Recon Agent、FOFA、Shodan、HTTP metadata、subfinder 和 Tscan，运行未取消的服务，断言发布的 `session.json` 包含 status `done` 的 Scan phase，且摘要为 `Scan is disabled by configuration.`。在 `artifacts_test.go` 将 fixture 对象的 Scan phase 也按既有 phase 顺序标记为 done，并增加一个具有 `Evidence: "evidence/scan-sqli-001.json"` 的 Finding。断言 Markdown 包含该 evidence 路径。

- [ ] **Step 2: 运行并确认失败**

Run:

```bash
go test ./internal/redteam ./internal/app ./internal/report -run 'TestNewEngagementSession|TestReconPipelineRunsScanBeforeVerify|TestReconPipelineCancelsScan|TestReconPipelineFailsScan|TestService.*Scan|TestWriteArtifacts' -count=1
```

Expected: FAIL，因为 `scan` phase、`ScanRunner` 注入点和 Scan Finding 投影还不存在。

- [ ] **Step 3: 实现会话与管线接入**

将 `MVPPhases` 改为下列固定顺序，其他 `Session` 状态机无需增加特殊分支：

```go
var MVPPhases = []string{"recon", "scan", "verify", "persist"}
```

在 `ReconPipeline` 新增 `scanRunner ScanRunner`，将构造函数替换为上述签名，并将 `Run` 的固定调度替换为：

```go
if err := p.runRecon(ctx, sess); err != nil {
	return err
}
if err := p.runScan(ctx, sess); err != nil {
	return err
}
return p.runVerify(ctx, sess)
```

`runScan` 必须先 `StartPhase("scan", p.clock())`、输出 `Phase [SCAN] started`、调用 `checkContext`，然后校验 `scanRunner != nil`。它使用下列输入调用 Scan runner：

```go
input := scan.Input{
	CanonicalTarget: sess.TargetInfo.URL.String(),
	Candidates:      scanCandidates(sess.TargetInfo, sess.Authorization, sess.Recon.Snapshot(), 0),
}
result, err := p.scanRunner.Run(ctx, input)
```

如果 `err` 或 `ctx.Err()` 为 `context.Canceled` 或 `context.DeadlineExceeded`，调用 `sess.Cancel(p.clock(), err)` 并返回 `err`；其他错误调用 `p.fail(sess, "scan", err)`。成功时将 `result.Findings` 依次经 `scanFinding(len(sess.Findings)+1, finding, p.clock())` 和 `sess.RecordFinding("scan", ...)` 写入会话。`scanFinding` 仅接受 `info`、`low`、`medium`、`high`、`critical`，其他字符串映射 `SeverityInfo`；它深拷贝 metadata，且以 `fmt.Sprintf("finding-%03d", index)` 生成 ID。

使用 `result.Summary` 作为 Scan phase 摘要；如果它为空，使用：

```go
summary := fmt.Sprintf("Completed %d Scan call(s); recorded %d finding(s).", len(result.Calls), len(result.Findings))
```

完成后输出 `Phase [SCAN] completed: ` 加摘要，再 `FinishPhase("scan", summary, p.clock())`。

在 `Service.Run` 创建 Recon runner 后创建 Scan runner，并以新参数组装 Pipeline：

```go
scanRunner := s.newScanRunner(writer)
pipeline := redteam.NewReconPipeline(runner, scanRunner, s.deps.Clock, func(message string) {
	progress(Event{Message: message})
})
```

`newScanRunner` 使用 `skill.LocalExecutor{}`、`&http.Client{Timeout: time.Duration(s.cfg.Scan.HTTPProbe.TimeoutSeconds) * time.Second}`、writer、`s.deps.Clock` 和日志回调创建 `scan.NewRunner`。它不读取 Agent 配置、不接收模型输入。

在 `README.md` 将流程更新为 `recon -> scan -> verify -> persist`，并追加与 `ScanConfig` 字段完全一致的 JSON 示例；说明 nuclei 固定扫 canonical target，HTTP 初筛只处理同源 sitemap URL。

- [ ] **Step 4: 验证通过并提交**

Run:

```bash
gofmt -w internal/redteam/session.go internal/redteam/recon_pipeline.go internal/app/engagement.go tests/_packages/internal/redteam/session_state_test.go tests/_packages/internal/redteam/recon_pipeline_test.go tests/_packages/internal/app/engagement_test.go tests/_packages/internal/report/artifacts_test.go
go test ./internal/redteam ./internal/app ./internal/report -count=1
git diff --check
git status --short
```

Expected: Session 以 Scan 作为第二个 phase 正确完成，报告出现 Scan evidence 引用。当前工作区已含有这些文件的未提交改动，此步不暂存也不提交它们，避免将既有改动混入 Scan 提交。

### Task 5: 全量验证与范围检查

**Files:**
- Modify only when verification identifies a factual mismatch: `README.md`
- Modify only when verification identifies a factual mismatch: `docs/superpowers/specs/2026-07-16-scan-baseline-design.md`

**Consumes:** Tasks 1-4 的实现。

**Produces:** 在当前 Go 版本下通过的 Scan 阶段，且范围与设计保持一致。

- [ ] **Step 1: 运行完整测试与静态检查**

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
go mod verify
```

Expected: 全部命令以退出码 0 结束。

- [ ] **Step 2: 检查固定范围、调度顺序和未跟踪文件**

Run:

```bash
rg -n 'MVPPhases|runScan|NewReconPipeline|scanCandidates' internal/redteam internal/app
rg -n 'nmap|Scan Agent|Tscan.*URL.*parse' internal/redteam README.md docs/superpowers/specs/2026-07-16-scan-baseline-design.md
git diff --check
git status --short
```

Expected: 第一条命令显示 `recon`、`scan`、`verify`、`persist` 的顺序和 Scan 调度点；第二条命令仅出现在设计的范围排除说明中，不出现在生产 Go 代码中；`git diff --check` 无输出；列表中仍保留用户未提交的文件，不将它们加入 Scan 提交。

- [ ] **Step 3: 处理已发现的不一致并重新验证**

如果 Task 5 的命令显示 README 或设计与实现不符，只在该精确文件修正下列事实：Scan 顺序、固定 nuclei 参数、同源 sitemap 候选规则、SQLi/XSS 初筛判定条件与 evidence 命名。不在这一步新增扫描能力或改变对外配置结构。然后重跑上一步的全部命令。

- [ ] **Step 4: 复核未提交文档修正**

Run:

```bash
git diff --check
git diff -- README.md docs/superpowers/specs/2026-07-16-scan-baseline-design.md
git status --short README.md docs/superpowers/specs/2026-07-16-scan-baseline-design.md
```

Expected: 没有空白错误。如果 README 含有本轮以前的未提交改动，保持其为未暂存状态；不在本计划中创建会混合既有改动的文档提交。
