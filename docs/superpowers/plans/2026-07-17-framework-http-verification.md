# 框架自发请求验证引擎实施计划（复刻 bingo VerificationEngine）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 复刻 bingo 的反幻觉核心——**框架自己发请求 + 确定性打分**。engagement 主循环结束后，模型把自认已证实的发现整理成结构化 `FindingSpec`（含 payload 请求与对照请求）。PentGo **用自己的 `net/http` 客户端**重新发起 payload 请求与对照请求、捕获目标真实字节、对响应跑确定性签名比对与因果隔离，产出 `Verdict`（VERIFIED/LIKELY/INCONCLUSIVE/REFUTED）+ 置信度。用于 SRC 批量刷洞时压制误报。

## 为什么这样能打破幻觉循环（设计依据）

模型若同时掌控「声明漏洞 / 写验证脚本 / 判定成功」，就是自证循环——它能幻觉漏洞，也能幻觉一段"打印成功标记"的脚本骗过检查。**唯一模型伪造不了的是目标实际回的字节，以及不同输入下的行为差异。**

bingo 的做法（已读源码核实，非推测）：
- `redteam/verification.py` 的 `VerificationEngine.verify(Evidence)` 是**纯确定性打分**，对 `response_raw` vs `baseline_response` 跑 vuln-type 签名 + 因果隔离 + 复现次数。
- 证据字节由**框架工具**（70 个 tool 用 `requests`/`httpx`）发起，不是模型打印的 stdout。

PentGo 复刻要点：**判定字节必须来自 PentGo 自己的 HTTP 客户端**（本计划 Task 2），模型只声明"测什么"，框架拥有"目标实际回了什么"和"最终判定"。若目标验证时不可达 → 证据为空 → INCONCLUSIVE（诚实，不误报为已验证）。

## bingo 五原则打分（照搬权重，Task 1）

| 原则 | 权重 | 检查 |
|---|---|---|
| P5 确定性优先 | **0.4** | vuln-type 正则作用于 response 且 baseline 无：SQLi 报错串 / XSS payload 原样反射 / LFI `root:x:0:0:` / RCE `uid=\d+\(` |
| P1 可复现 | 0.25 | ≥3 次独立重放 |
| P2 因果隔离 | 0.2 | baseline 响应 ≠ payload 响应 |
| P3 窄问题 | 0.1 | 单变量 payload |
| P4 生成≠验证 | 0.05 | 有独立验证轮次 |

判定：`≥0.75 VERIFIED`｜`≥0.45 LIKELY`｜`≥0.2 INCONCLUSIVE`｜`else REFUTED`。

## 架构

```
runner.Run 结束 (SessionDone)
  └─ ConsolidateAndVerify(ctx, session, verifier)
       ├─ 1 次模型调用: 让模型输出 === PENTGO FINDING === 结构块 (payload + baseline)
       ├─ parseFindingSpecs -> []FindingSpec
       └─ 对每个 spec (受 MaxFindings 限):
            HTTPVerifier.Verify         [框架自发请求, 走 Scope host 门]
              ├─ 发 baseline 请求 -> 捕获对照字节
              ├─ 发 payload 请求 xN -> 捕获响应字节 + 复现计数
              └─ Score(Evidence) -> VerificationResult
  -> ReportContext.VerifiedFindings
  -> 报告模型据"框架已验证事实"撰写 (VERIFIED 列入正式发现, 附 curl)
```

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`；仅用标准库（`net/http`、`regexp`、`strings`）。
- 生产源码放 `internal/runtime/`、`internal/report/`、`internal/app/`、`internal/config/`；测试真身放 `tests/_packages/<same-rel-path>/`，源码目录建**相对符号链接**（如 `internal/runtime/verification_test.go -> ../../tests/_packages/internal/runtime/verification_test.go`）。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- **验证请求只发往已授权范围**：`HTTPVerifier.Verify` 必须先 `scope.HostAllowed(host)`，越界返回 INCONCLUSIVE + 记原因，不发请求。
- **幂等安全**：GET/HEAD 可重放 N 次；POST/PUT/DELETE/PATCH **只发 1 次**（避免重复副作用），复现分不计满，记 note。
- 验证阶段失败/超时/为空 → 该发现 INCONCLUSIVE，绝不升级为 VERIFIED。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

---
### Task 1: 确定性打分引擎 `verification.go`（纯函数，TDD）

**Files:**
- Create: `internal/runtime/verification.go`
- Create: `tests/_packages/internal/runtime/verification_test.go`
- Create symlink: `internal/runtime/verification_test.go -> ../../tests/_packages/internal/runtime/verification_test.go`

**Interfaces (Produces):**
- `type Verdict string`（`VerdictVerified/VerdictLikely/VerdictInconclusive/VerdictRefuted`）
- `type VulnType string`（`sqli/xss/lfi/rce/auth_bypass/upload/open_redirect`）
- `type Evidence struct { VulnType VulnType; Payload, ResponseBody, BaselineBody, LocationHeader string; StatusCode, BaselineStatus, ReproductionCount int }`
- `type VerificationResult struct { Verdict Verdict; VulnType VulnType; Confidence float64; ChecksPassed, ChecksFailed []string; Summary string }`
- `func Score(evidence Evidence) VerificationResult`
- `func deterministicCheck(evidence Evidence) (bool, string)`（unexported，按 vuln-type 分发）

- [ ] **Step 1: Write the failing tests**

`tests/_packages/internal/runtime/verification_test.go`（要点，执行者补全所有分支）：

```go
package runtime

import "testing"

func TestScoreSQLiErrorAgainstBaselineVerified(t *testing.T) {
    ev := Evidence{
        VulnType:          VulnSQLI,
        Payload:           "id=1'",
        ResponseBody:      "You have an error in your SQL syntax near '1''",
        BaselineBody:      "welcome user 1",
        StatusCode:        500,
        BaselineStatus:    200,
        ReproductionCount: 3,
    }
    r := Score(ev)
    if r.Verdict != VerdictVerified {
        t.Fatalf("verdict = %s, confidence = %.2f, failed = %v", r.Verdict, r.Confidence, r.ChecksFailed)
    }
}

func TestScoreSQLiErrorAlsoInBaselineNotDeterministic(t *testing.T) {
    // 报错串在 baseline 也出现 -> P5 不通过 (非注入所致)
    ev := Evidence{
        VulnType:          VulnSQLI,
        Payload:           "id=1'",
        ResponseBody:      "You have an error in your SQL syntax",
        BaselineBody:      "You have an error in your SQL syntax",
        ReproductionCount: 3,
    }
    r := Score(ev)
    if r.Verdict == VerdictVerified {
        t.Fatalf("must not verify when error also present in baseline: %+v", r)
    }
}

func TestScoreXSSReflectedVerbatim(t *testing.T) {
    ev := Evidence{
        VulnType:          VulnXSS,
        Payload:           "<script>alert(1)</script>",
        ResponseBody:      "<div><script>alert(1)</script></div>",
        BaselineBody:      "<div>normal</div>",
        ReproductionCount: 3,
    }
    if Score(ev).Verdict != VerdictVerified {
        t.Fatalf("verbatim reflected XSS should verify")
    }
}

func TestScoreLFIPasswd(t *testing.T) {
    ev := Evidence{VulnType: VulnLFI, Payload: "../../etc/passwd", ResponseBody: "root:x:0:0:root:/root:/bin/bash", BaselineBody: "not found", ReproductionCount: 3}
    if Score(ev).Verdict != VerdictVerified {
        t.Fatalf("LFI passwd signature should verify")
    }
}

func TestScoreRCEIdOutput(t *testing.T) {
    ev := Evidence{VulnType: VulnRCE, Payload: ";id", ResponseBody: "uid=33(www-data) gid=33(www-data)", BaselineBody: "", ReproductionCount: 3}
    if Score(ev).Verdict != VerdictVerified {
        t.Fatalf("RCE id output should verify")
    }
}

func TestScoreOpenRedirectLocationOffsite(t *testing.T) {
    ev := Evidence{VulnType: VulnOpenRedirect, Payload: "next=//evil.example", LocationHeader: "https://evil.example/", StatusCode: 302, ReproductionCount: 3}
    if v := Score(ev).Verdict; v != VerdictVerified && v != VerdictLikely {
        t.Fatalf("offsite Location redirect should verify/likely, got %s", v)
    }
}

func TestScoreNoEvidenceRefuted(t *testing.T) {
    ev := Evidence{VulnType: VulnSQLI, Payload: "id=1'", ResponseBody: "welcome", BaselineBody: "welcome", ReproductionCount: 0}
    if Score(ev).Verdict != VerdictRefuted {
        t.Fatalf("no evidence should be refuted")
    }
}

func TestScoreReproductionAndCausalContributeConfidence(t *testing.T) {
    // 仅有 baseline 差异 + 复现，无确定性签名 -> LIKELY 区间，不 VERIFIED
    ev := Evidence{VulnType: VulnSQLI, Payload: "id=1'", ResponseBody: "AAA different", BaselineBody: "BBB", ReproductionCount: 3}
    r := Score(ev)
    if r.Verdict == VerdictVerified {
        t.Fatalf("no deterministic signature must not reach VERIFIED: %+v", r)
    }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/runtime -run TestScore -timeout 30s -count=1`
Expected: FAIL（`Score`、`Evidence` 等未定义）。

- [ ] **Step 3: Write implementation `internal/runtime/verification.go`**

照搬 bingo `verification.py` 的权重与签名（用 Go regexp）：

- `Verdict`/`VulnType` 常量。
- `deterministicCheck`：按 `VulnType` 分发，全部**要求签名在 response 命中且 baseline 未命中**（因果）：
  - `VulnSQLI`：`sql syntax|mysql_fetch|you have an error|ora-|pg::|sqlite_|1064` 命中 response 且不在 baseline；或 payload 含 `union` 且 `len(resp) > len(base)*1.5`。
  - `VulnXSS`：`payload` 原样出现在 response（且 baseline 无）；或 `<script|onerror=|onload=|javascript:` 反射。
  - `VulnLFI`：`root:x:0:0:` / `\[drivers\]` / `<?php` / `define\s*\(`。
  - `VulnRCE`：`uid=\d+\(` / `Volume Serial Number` / `NT AUTHORITY\\SYSTEM`。
  - `VulnAuthBypass`：admin 指示词在 response 且不在 baseline；或 `BaselineStatus==302 && StatusCode==200`。
  - `VulnUpload`：`"success"\s*:\s*true` / `file.*uploaded` / 可执行扩展 URL。
  - `VulnOpenRedirect`：`LocationHeader` 解析出的 host 非空且 ≠ 目标 host（离站跳转）。
- `Score`：
  ```
  confidence := 0.0
  if det { +0.4; passed } else { failed }
  if ReproductionCount >= 3 { +0.25 } else { failed }
  if BaselineBody != "" && ResponseBody != "" && BaselineBody != ResponseBody { +0.2 } else if BaselineBody=="" { failed "no baseline" } else { failed "no diff" }
  if strings.Count(Payload,"&") <= 1 { +0.1 }   // 窄问题近似
  if ReproductionCount > 0 { +0.05 }
  confidence = min(confidence, 1.0)
  verdict: >=0.75 V | >=0.45 L | >=0.2 INC | else REFUTED
  ```
- 注意：开放重定向类以 `LocationHeader` 为主证据，Score 里对 `deterministicCheck` 已含该判定，其余因果/复现照常累加。

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/runtime -run TestScore -timeout 30s -count=1` → PASS
整包回归：`go test ./internal/runtime -timeout 60s -count=1` → PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/verification.go tests/_packages/internal/runtime/verification_test.go
git add internal/runtime/verification.go internal/runtime/verification_test.go tests/_packages/internal/runtime/verification_test.go
git commit -m "feat: add deterministic vulnerability scoring engine"
```

---

### Task 2: 框架自发请求 `HTTPVerifier`（TDD，用 httptest）

**Files:**
- Create: `internal/runtime/http_verifier.go`
- Create: `tests/_packages/internal/runtime/http_verifier_test.go`
- Create symlink: `internal/runtime/http_verifier_test.go -> ../../tests/_packages/internal/runtime/http_verifier_test.go`

**Interfaces (Produces):**
- `type FindingSpec struct { VulnType VulnType; Method, URL, BaselineURL, Body, BaselineBody, Payload, Severity, Description string; Headers map[string]string }`
- `type HTTPVerifier struct { client *http.Client; scope Scope; reproductions int; maxBodyBytes int }`
- `func NewHTTPVerifier(client *http.Client, scope Scope, reproductions int) *HTTPVerifier`
- `func (v *HTTPVerifier) Verify(ctx context.Context, spec FindingSpec) VerificationResult`
- `func CurlCommand(spec FindingSpec) string`（报告复现用）

**Behavior:**
- host 门：`spec.URL` 与 `spec.BaselineURL` 的 host 都必须 `scope.HostAllowed`，否则不发请求、返回 `INCONCLUSIVE` + `ChecksFailed=["scope: host out of authorized range"]`。
- 发 baseline 请求（若 `BaselineURL` 为空则用 `URL` 去掉 payload 的对照；最简实现：`BaselineURL` 必填由模型给，缺失则 baseline 留空 → P2 记 no baseline）。
- 发 payload 请求：GET/HEAD 重放 `reproductions` 次（默认 3），非幂等方法只发 1 次并在 result 记 note。
- 读取响应体用 `io.LimitReader(body, maxBodyBytes+1)`（默认 64KB），捕获 `StatusCode`、`Location` 头。
- 组装 `Evidence` 交 `Score`。请求错误/超时 → INCONCLUSIVE。

- [ ] **Step 1: Write failing tests（httptest 起本地服务器，同源放行私网）**

```go
func TestHTTPVerifierConfirmsReflectedXSS(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query().Get("q")
        if q != "" {
            w.Write([]byte("<div>" + q + "</div>"))
            return
        }
        w.Write([]byte("<div>home</div>"))
    }))
    defer srv.Close()
    scope := NewScope(hostOf(srv.URL), nil, true) // allowPrivate=true for 127.0.0.1
    v := NewHTTPVerifier(srv.Client(), scope, 3)
    r := v.Verify(context.Background(), FindingSpec{
        VulnType:    VulnXSS,
        Method:      "GET",
        URL:         srv.URL + "/?q=<script>alert(1)</script>",
        BaselineURL: srv.URL + "/?q=benign",
        Payload:     "<script>alert(1)</script>",
    })
    if r.Verdict != VerdictVerified {
        t.Fatalf("verdict = %s failed = %v", r.Verdict, r.ChecksFailed)
    }
}

func TestHTTPVerifierRejectsOutOfScope(t *testing.T) {
    scope := NewScope("target.example", nil, false)
    v := NewHTTPVerifier(http.DefaultClient, scope, 3)
    r := v.Verify(context.Background(), FindingSpec{VulnType: VulnXSS, Method: "GET", URL: "https://evil.example/?q=x", Payload: "x"})
    if r.Verdict != VerdictInconclusive {
        t.Fatalf("out-of-scope must be inconclusive, got %s", r.Verdict)
    }
}

func TestHTTPVerifierNonIdempotentSingleRequest(t *testing.T) {
    hits := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == "POST" { hits++ }
        w.Write([]byte("ok"))
    }))
    defer srv.Close()
    scope := NewScope(hostOf(srv.URL), nil, true)
    v := NewHTTPVerifier(srv.Client(), scope, 3)
    v.Verify(context.Background(), FindingSpec{VulnType: VulnUpload, Method: "POST", URL: srv.URL + "/upload", Payload: "x"})
    if hits != 1 {
        t.Fatalf("POST replayed %d times, want 1 (idempotency guard)", hits)
    }
}
```

- [ ] **Step 2: Run to verify fails** → `go test ./internal/runtime -run TestHTTPVerifier -count=1`（未定义，FAIL）

- [ ] **Step 3: Implement `http_verifier.go`**

用 `http.NewRequestWithContext`，`io.LimitReader` 读体（照 `internal/agent/types.go:83` 风格）。幂等判断：`method == GET || HEAD` 才循环 `reproductions` 次，其余 `n=1`。`ReproductionCount` = 实际成功发出的 payload 请求数。

- [ ] **Step 4: Run to verify passes** → PASS + 整包回归

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/http_verifier.go tests/_packages/internal/runtime/http_verifier_test.go
git add internal/runtime/http_verifier.go internal/runtime/http_verifier_test.go tests/_packages/internal/runtime/http_verifier_test.go
git commit -m "feat: framework-owned HTTP verifier issues payload and baseline requests"
```

---

### Task 3: FindingSpec 声明格式 + 解析器（TDD，纯函数）

**Files:**
- Create: `internal/runtime/finding_spec.go`
- Create: `tests/_packages/internal/runtime/finding_spec_test.go` + symlink

**声明格式**（写进 consolidation 提示词，模型输出；line-based，与 SKILL_LOAD 风格一致）：

```
=== PENTGO FINDING ===
type: sqli
severity: high
method: GET
url: https://t.example/item?id=1'
baseline_url: https://t.example/item?id=1
payload: id=1'
description: id 参数报错型 SQL 注入
=== END PENTGO FINDING ===
```

POST 追加 `body:` / `baseline_body:` / `header: K: V`（可多行）。

**Interfaces:**
- `func ParseFindingSpecs(text string) []FindingSpec`（未知 type 跳过；缺 url 跳过；去重）

- [ ] **Step 1: failing tests** — 覆盖：单个 GET spec、POST 带 body+header、多个 spec、未知 type 跳过、缺 url 跳过、无块返回 nil。
- [ ] **Step 2: run fail**
- [ ] **Step 3: implement**（正则切块 `(?s)=== PENTGO FINDING ===(.*?)=== END PENTGO FINDING ===`，逐行 `key: value`）
- [ ] **Step 4: run pass** + 整包回归
- [ ] **Step 5: commit** `feat: parse structured finding declarations from model output`

---

### Task 4: Runner 编排 `ConsolidateAndVerify` + 配置（TDD）

**Files:**
- Modify: `internal/runtime/runner.go`（加 `findings []VerificationResult` + `findingSpecs []FindingSpec` 字段、`ConsolidateAndVerify` 方法、`MaxFindings`/`Verifier` 进 `RunnerConfig`）
- Modify: `internal/config/config.go`（`MaxFindings int` 默认 10 + normalize；`VerificationReproductions int` 默认 3）
- Test: `tests/_packages/internal/runtime/runner_test.go`（追加）、`tests/_packages/internal/config/config_test.go`（追加）

**Interfaces (Produces):**
- `RunnerConfig` 新增 `MaxFindings int`、`Verifier FindingVerifier`（接口 `Verify(context.Context, FindingSpec) VerificationResult`，便于测试注入假 verifier）
- `func (runner *Runner) ConsolidateAndVerify(ctx context.Context, session *AgentSession) []VerificationResult`
  - 用 `runner.history`（改为 Runner 字段，Run 内赋值）追加 consolidation 指令，1 次 `runner.chat`
  - `ParseFindingSpecs` → 受 `MaxFindings` 截断
  - 逐 spec 调 `runner.config.Verifier.Verify`（nil 则跳过、返回空）
  - 存入 `runner.findings`，供 `ReportContext` 读取

- [ ] **Step 1: failing tests** — 用 `scriptedClient`（consolidation 轮返回两个 `=== PENTGO FINDING ===` 块）+ 假 `FindingVerifier`（一个返 VERIFIED、一个返 REFUTED）；断言 `ConsolidateAndVerify` 返回 2 条且 verdict 正确、`MaxFindings=1` 时只验 1 条。config 默认值测试。
- [ ] **Step 2: run fail**
- [ ] **Step 3: implement**：`history` 提升为 Runner 字段；consolidation 提示词要求模型对每个已用执行证据证实的漏洞输出 FINDING 块、无则输出空。
- [ ] **Step 4: run pass** + 整包回归
- [ ] **Step 5: commit** `feat: consolidate and verify findings after engagement loop`

---

### Task 5: 报告接线 + 全量验证

**Files:**
- Modify: `internal/runtime/report_context.go`（`ReportContext` 加 `VerifiedFindings []VerificationResult`；`PromptText` 增"框架验证发现"节，含 verdict/confidence/curl，复用字节预算）
- Modify: `internal/runtime/runner.go`（`ReportContext()` 填充 `VerifiedFindings`；`CurlCommand` 生成复现命令）
- Modify: `internal/report/generator.go`（系统提示词：**只有 framework Verdict=VERIFIED 的项列入"已验证发现"**；LIKELY 入"疑似"；INCONCLUSIVE/REFUTED 入"未完成或受阻"）
- Modify: `internal/app/engagement.go`（`runner.Run` 后、报告前调用 `runner.ConsolidateAndVerify(ctx, session)`；构造 `HTTPVerifier` 用 engagement 的 scope + 一个带超时的 `http.Client`，接线进 `RunnerConfig.Verifier`、`MaxFindings`）
- Modify: `cmd/pentgo/main_test.go` 的 model fixture（补 consolidation 轮响应，返回一个 FINDING 或空块）

- [ ] **Step 1**: report_context 测试：`VerifiedFindings` 含 VERIFIED 项时 `PromptText` 出现 curl 与 "框架已验证"；generator 测试：VERIFIED 项进"已验证发现"。
- [ ] **Step 2**: 接线 engagement + 修 cmd fixture。
- [ ] **Step 3: 全量回归**
  ```bash
  go build ./...
  go test ./... -timeout 120s
  go vet ./...
  git diff --check
  ```
- [ ] **Step 4: 端对端冒烟（授权目标）**：跑一次真实 engagement，确认 report.md 的"已验证发现"仅含框架自发请求确认的项、每条带 curl；验证阶段对不可达/无差异项正确标 INCONCLUSIVE 而非误报。
- [ ] **Step 5: commit** `feat: wire framework verification into report pipeline`

---

## 自查

- **打破循环**：判定字节来自 `HTTPVerifier` 自发请求（Task 2），非模型 stdout；`Score`（Task 1）纯确定性；模型只声明 spec（Task 3），不参与判定。
- **误报控制**：仅 framework `Verdict=VERIFIED` 进"已验证发现"；不可达/无 baseline 差异/无签名 → 降级，不升级。
- **安全**：验证请求受 `scope.HostAllowed` 门；非幂等方法不重放；沿用授权范围。
- **类型一致性**：`VulnType`/`Verdict` 单一定义（Task 1）贯穿 spec/verifier/report；`FindingSpec` 在 Task 2 定义、Task 3 解析、Task 4 消费；`MaxFindings`/`VerificationReproductions` 在 config↔RunnerConfig↔engagement 三处一致。
- **占位符**：无 TODO；提示词与解析器键一一对应。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接，改断言改真身。
2. `FindingSpec` 与 `VulnType`/`Verdict` 建议都放在 Task 1 的 `verification.go`（或 spec 放 Task 3 文件）以避免循环；执行时保持单一定义、其余文件引用。
3. `history` 提升为 `Runner` 字段后，`Run` 内所有 `history.` 调用改用字段；确认并发安全（engagement 串行，无并发）。
4. consolidation 是 `Run` 之后的独立一次模型调用；仅在 `session.Status == SessionDone` 时执行（失败/取消不整理发现）。
5. `HTTPVerifier` 的 `http.Client` 需独立超时（如 15s），不复用 agent client。
6. Task 顺序有依赖：T1→T2→T3→T4→T5，逐个提交。

