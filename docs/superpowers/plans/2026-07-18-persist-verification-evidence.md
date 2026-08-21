# 持久化验证证据 + 数据驱动报告发现章节 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐反幻觉体系里最后的审计断裂——把框架自发请求拿到的 payload/baseline **原始字节**落盘为 `evidence/verification-*.json`，并将 `[]VerificationResult` 持久化进 `session.json`。随后把报告的"已验证发现"章节改为**从持久化结构确定性渲染**（数据驱动，零模型参与），模型 markdown 仅负责叙述性章节。

## 问题（已核对代码，非推测）

1. **审计断裂**：`runner.findings []VerificationResult` 仅存于内存（runner.go:70）；`session.json` marshal 的 `AgentSession`（session.go:22-33）**无 findings 字段**。框架自发请求拿到的 payload/baseline 字节——反幻觉体系里**唯一模型伪造不了的证据**——进了报告提示词后即蒸发。反观模型自己写的执行块 stdout（**可被模型操纵**）却落盘进 `evidence/*.json`。最可信的证据没留档，较不可信的留了，方向是反的。
   - 对 SRC 致命：`VerificationResult.Curl`（verification.go:63）已有复现命令，但对应的**目标实际响应字节**没存，人工/平台复核时无法离线比对"框架当时看到的字节"。
   - `HTTPVerifier.Verify`（http_verifier.go:64）当前只返回 `VerificationResult`，**丢弃了它自己捕获的 request/response 字节**（`httpVerificationResponse` 是内部类型，未外泄）。要落盘必须先让这些字节可取。

2. **报告纯提示词约束**：`GenerateTerminalMarkdown`（generator.go）发布模型 markdown 原文。虽然提示词已喂确定性 VERDICT+confidence+curl（report_context.go:69），但没有任何东西保证模型按章节契约输出，事实性章节仍由模型笔写。

## 现有可复用基建

- `EvidenceSink` 接口（executor.go:42）：`WriteEvidence(name string, value any) (string, error)`，返回相对路径。`EngagementWriter` 已实现（artifacts.go:82），命名规则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`，重名报错。执行块用 `agent-turn-%03d-block-%03d`（executor.go:154）。
- `renderMarkdown`（markdown.go）：确定性时间线渲染器，已是数据驱动风格的范例。
- `ConsolidateAndVerify`（runner.go:297）：逐 spec 调 `Verifier.Verify`，收集进 `runner.findings`。落盘点就在此循环内。

## 架构

```
ConsolidateAndVerify (runner.go)
  └─ 每个 spec:
       verifier.VerifyWithEvidence(ctx, spec)  ← 新: 返回 (VerificationResult, VerificationRecord)
         · VerificationRecord 含 request/payload-response/baseline-response 原始字节
       ├─ runner.findings = append(...)                     [报告用]
       └─ sink.WriteEvidence("verification-001", record)    [审计落盘, 新]
  session.Findings = runner.findings                         [进 session.json, 新]

report 发布 (engagement.go / artifacts.go)
  └─ report.md = 确定性"已验证发现"章节(从 session.Findings 渲染)
                 + 模型 markdown 的叙述性章节
```

## Global Constraints

- 模块名 `pentgo`；Go 1.25.0；仅标准库。
- 生产源码放 `internal/runtime|report|app/`；测试真身放 `tests/_packages/<same-rel-path>/`，源码目录建**相对符号链接**。
- 测试 package 与被测包同名，不用外部 `_test`。
- **落盘的验证证据必须含原始 request/response 字节**（payload + baseline），受 `maxBodyBytes` 上限约束（复用 http_verifier 的 64KB）。
- **不改判定逻辑**：`Score` 与 verdict 阈值不动；本计划只做持久化与渲染。
- 验证证据落盘失败不得中断 engagement（记 timeline 事件，继续发布其余 artifacts）。
- 每步结束运行该步测试；每个 Task 至少一次提交。

---

### Task 1: `HTTPVerifier` 暴露捕获的原始字节（TDD）

**Files:**
- Modify: `internal/runtime/http_verifier.go`
- Test: `tests/_packages/internal/runtime/http_verifier_test.go`（追加）

**Interfaces (Produces):**
- `type VerificationRecord struct { Method, PayloadURL, BaselineURL string; RequestHeaders map[string]string; RequestBody, BaselineRequestBody string; PayloadStatus int; PayloadResponseBody, PayloadLocation string; BaselineStatus int; BaselineResponseBody string; Reproductions int; ScopeRejected bool }`
- `func (verifier *HTTPVerifier) VerifyWithEvidence(ctx context.Context, spec FindingSpec) (VerificationResult, VerificationRecord)`
- 保留现有 `Verify(ctx, spec) VerificationResult` 作为薄封装（`r, _ := VerifyWithEvidence(...); return r`），使既有测试不变。

**Behavior:** `VerificationRecord` 记录框架实际发出的请求与目标实际返回的字节（截断到 `maxBodyBytes`）。scope 拒绝时 `ScopeRejected=true`、body 字段留空。

- [ ] **Step 1: Write failing tests**

```go
func TestVerifyWithEvidenceCapturesRawBytes(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query().Get("q")
        if q != "" { w.Write([]byte("<div>" + q + "</div>")); return }
        w.Write([]byte("<div>home</div>"))
    }))
    defer srv.Close()
    scope := NewScope(hostOf(srv.URL), nil, true)
    v := NewHTTPVerifier(srv.Client(), scope, 3)
    res, rec := v.VerifyWithEvidence(context.Background(), FindingSpec{
        VulnType: VulnXSS, Method: "GET",
        URL:         srv.URL + "/?q=<script>alert(1)</script>",
        BaselineURL: srv.URL + "/?q=benign",
        Payload:     "<script>alert(1)</script>",
    })
    if res.Verdict != VerdictVerified {
        t.Fatalf("verdict = %s", res.Verdict)
    }
    if !strings.Contains(rec.PayloadResponseBody, "<script>alert(1)</script>") {
        t.Fatalf("payload response not captured: %q", rec.PayloadResponseBody)
    }
    if !strings.Contains(rec.BaselineResponseBody, "benign") {
        t.Fatalf("baseline response not captured: %q", rec.BaselineResponseBody)
    }
    if rec.PayloadStatus != 200 || rec.Reproductions != 3 {
        t.Fatalf("status/reproductions = %d/%d", rec.PayloadStatus, rec.Reproductions)
    }
}

func TestVerifyWithEvidenceScopeRejectedRecord(t *testing.T) {
    scope := NewScope("target.example", nil, false)
    v := NewHTTPVerifier(http.DefaultClient, scope, 3)
    res, rec := v.VerifyWithEvidence(context.Background(), FindingSpec{VulnType: VulnXSS, Method: "GET", URL: "https://evil.example/?q=x", Payload: "x"})
    if res.Verdict != VerdictInconclusive || !rec.ScopeRejected {
        t.Fatalf("res=%s scopeRejected=%v", res.Verdict, rec.ScopeRejected)
    }
    if rec.PayloadResponseBody != "" {
        t.Fatalf("must not capture body when scope rejected")
    }
}

func TestVerifyStillReturnsResultOnly(t *testing.T) {
    // 既有薄封装签名不变
    scope := NewScope("target.example", nil, false)
    v := NewHTTPVerifier(http.DefaultClient, scope, 3)
    if v.Verify(context.Background(), FindingSpec{VulnType: VulnXSS, Method: "GET", URL: "https://evil.example/", Payload: "x"}).Verdict != VerdictInconclusive {
        t.Fatal("thin wrapper broke")
    }
}
```

- [ ] **Step 2: Run to verify fails** — `go test ./internal/runtime -run "VerifyWithEvidence|VerifyStill" -count=1`
- [ ] **Step 3: Implement** — 把 `Verify` 主体重构进 `VerifyWithEvidence`，沿途填充 `VerificationRecord`（在发 baseline/payload 请求处记录 URL、status、body、location）；`Verify` 变薄封装。scope 拒绝分支置 `ScopeRejected=true`。
- [ ] **Step 4: Run to verify passes** + 整包回归 `go test ./internal/runtime -count=1`
- [ ] **Step 5: Commit** `feat: expose captured request/response bytes from HTTP verifier`

---

### Task 2: `session.json` 持久化 Findings（TDD）

**Files:**
- Modify: `internal/runtime/session.go`（`AgentSession` 加字段）
- Modify: `internal/runtime/runner.go`（`ConsolidateAndVerify` 写入 `session.Findings`）
- Test: `tests/_packages/internal/runtime/runner_test.go`（追加）；`tests/_packages/internal/runtime/session_test.go`（若存在则追加，否则在 runner_test 覆盖）

**Interfaces (Produces):**
- `AgentSession` 新增：`Findings []VerificationResult \`json:"findings,omitempty"\``
- `ConsolidateAndVerify` 末尾：`session.Findings = append([]VerificationResult(nil), runner.findings...)`

- [ ] **Step 1: Write failing test** — 用 `scriptedClient`（consolidation 轮返回一个 FINDING 块）+ 假 `FindingVerifier`（返 VERIFIED）；`ConsolidateAndVerify` 后断言 `session.Findings` 长度 1、verdict=VERIFIED；再 `json.Marshal(session)` 断言输出含 `"findings"` 与 `"VERIFIED"`。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — session.go 加字段；runner.go 赋值。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: persist verification findings into session.json`

---

### Task 3: 落盘 `evidence/verification-*.json`（TDD）

**Files:**
- Modify: `internal/runtime/runner.go`（`ConsolidateAndVerify` 循环内落盘）
- Modify: `internal/runtime/runner.go` 或新增小类型：`verificationEvidence`（schema 包装，类比 `executionEvidence`）
- 需要：`RunnerConfig` 增 `EvidenceSink EvidenceSink`（可为 nil，测试注入假 sink），或复用现有 Verifier 传入。**决策：加 `RunnerConfig.EvidenceSink`**（与 executor 用同一 `EngagementWriter` 实例，见 Task 5 接线）。
- 需要：`Verifier` 接口从 `Verify` 升级为可取证据。**决策**：`RunnerConfig.Verifier` 改为接口 `FindingVerifier { VerifyWithEvidence(context.Context, FindingSpec) (VerificationResult, VerificationRecord) }`，`*HTTPVerifier` 已满足（Task 1）。假 verifier 测试同步更新。
- Test: `tests/_packages/internal/runtime/runner_test.go`

**Behavior:** 每个 spec 验证后，`sink.WriteEvidence(fmt.Sprintf("verification-%03d", i+1), verificationEvidence{...})`，把 `VerificationRecord` + verdict + confidence + checks 打包落盘。落盘错误记 timeline `verification_evidence_error` 但不中断。evidence 路径可选记入 `VerificationResult`（加 `EvidencePath string` 字段）便于报告引用。

- [ ] **Step 1: Write failing test** — 假 sink 记录收到的 `(name, value)`；`ConsolidateAndVerify` 后断言 sink 收到 `verification-001`、value 含捕获的 response body 字节。假 verifier 改实现 `VerifyWithEvidence`。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — 定义 `verificationEvidence` schema struct（`schema_version`、`vuln_type`、`verdict`、`confidence`、`method`、`payload_url`、`baseline_url`、`payload_status`、`payload_response_body`、`baseline_status`、`baseline_response_body`、`reproductions`、`checks_passed`、`checks_failed`、`curl`）；循环内落盘；`FindingVerifier` 接口切换。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: persist framework verification evidence to audit files`

---

### Task 4: 数据驱动的"已验证发现"报告章节（TDD）

**Files:**
- Modify: `internal/report/generator.go`（或新增 `internal/report/findings.go`）
- Modify: `internal/report/artifacts.go`（`PublishWithReport` 组装最终 markdown）
- Test: `tests/_packages/internal/report/*_test.go`

**决策**：不再让模型笔写事实性发现章节。最终 report.md =
1. **确定性渲染的"## 已验证发现"章节**（从 `session.Findings` 生成：仅 `VERIFIED` 列为确认漏洞、`LIKELY` 列为疑似、`INCONCLUSIVE/REFUTED` 列为"声明未获框架验证"；每条附 verdict/confidence/curl/evidence 路径）——数据驱动，零模型参与。
2. 模型 markdown 追加为叙述性部分（执行摘要、影响与修复建议、受阻项目）。

**Interfaces (Produces):**
- `func RenderVerifiedFindings(findings []runtime.VerificationResult) string`（确定性 markdown 段）
- `PublishWithReport` 签名扩展或内部拼接：`确定性发现段 + "\n" + 模型markdown`。若模型 markdown 为空/出错，回退仍产出确定性发现段 + 时间线。

- [ ] **Step 1: Write failing tests**
  - `RenderVerifiedFindings` 输入含一个 VERIFIED + 一个 INCONCLUSIVE，断言输出：VERIFIED 在"确认"小节含 curl，INCONCLUSIVE 在"未获验证"小节；空输入产出"未验证漏洞"。
  - `PublishWithReport` 断言最终 report.md 同时含确定性发现段与模型叙述段。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — 新渲染函数（风格照 markdown.go）；`PublishWithReport` 拼接。更新 generator 系统提示词：叙述性章节，事实性发现由框架渲染、模型勿重复列举 verdict。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: render verified findings deterministically in report`

---

### Task 5: 接线 + 全量验证

**Files:**
- Modify: `internal/app/engagement.go`（构造 `HTTPVerifier` 后接线 `RunnerConfig.EvidenceSink = writer`；`FindingVerifier` 接口已由 `*HTTPVerifier` 满足；`ConsolidateAndVerify` 已在 Run 后调用——确认顺序）
- Modify: `cmd/pentgo/main_test.go`（若发现章节改变了 report.md 前缀断言，同步）

- [ ] **Step 1**: engagement.go 把 `writer`（EngagementWriter，已实现 EvidenceSink）传入 `RunnerConfig.EvidenceSink`；确认 `ConsolidateAndVerify` 在 `runner.Run` 之后、报告生成之前调用（现状已如此，见现有接线）。
- [ ] **Step 2: 全量回归**
  ```bash
  go build ./...
  go test ./... -timeout 120s
  go vet ./...
  git diff --check
  ```
- [ ] **Step 3: 端对端冒烟（授权目标）**：跑一次真实 engagement，确认：
  - `evidence/verification-001.json` 存在且含 payload + baseline **原始响应字节**；
  - `session.json` 含 `findings` 数组与 verdict；
  - `report.md` 的"已验证发现"章节由框架数据渲染、VERIFIED 项带 curl，INCONCLUSIVE 项在"未获验证"小节。
- [ ] **Step 4: Commit** `feat: wire verification evidence sink into engagement`

---

## 自查

- **补上审计断裂**：payload/baseline 原始字节落盘 `verification-*.json`（Task 1+3）；findings 进 `session.json`（Task 2）。
- **可信证据优先渲染**：报告事实性章节数据驱动（Task 4），模型只写叙述——INCONCLUSIVE 不会被模型笔误升级。
- **不改判定**：`Score`/阈值不动；只做持久化与渲染。
- **健壮性**：落盘失败记事件、不中断发布；模型 markdown 为空仍产确定性发现段。
- **类型一致性**：`FindingVerifier` 接口（Task 3）由 `*HTTPVerifier`（Task 1）满足；`VerificationResult` 加 `EvidencePath` 后 report_context/generator/artifacts 同步；`AgentSession.Findings`（Task 2）↔ `RenderVerifiedFindings`（Task 4）字段一致。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接，改断言改真身；新增测试文件需建符号链接。
2. Task 3 把 `RunnerConfig.Verifier` 接口从 `Verify` 升为 `VerifyWithEvidence`——现有假 verifier 测试（runner_test.go）与 engagement.go 的赋值同步更新；`*HTTPVerifier` 经 Task 1 已满足新接口。
3. `EngagementWriter` 已实现 `EvidenceSink`（artifacts.go:82），Task 5 直接传实例，不新建。
4. evidence 命名 `verification-%03d` 匹配 `evidenceNamePattern`（合法）。
5. Task 顺序有依赖：T1（暴露字节）→T2（session 字段）→T3（落盘，依赖 T1 接口）→T4（渲染，依赖 T2 字段）→T5（接线）。逐个提交。
6. Task 4 若改变 report.md 起始内容，`cmd/pentgo/main_test.go` 里 `report.md` 前缀断言（`# 最终报告`）需同步。
