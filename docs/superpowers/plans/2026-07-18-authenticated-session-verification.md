# 认证会话 + Cookie Jar 验证 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给框架自发验证补上"认证身份"。当前 `HTTPVerifier` 是无状态的（无 cookie jar、无登录步），凡是需要已认证身份才能触发的发现——IDOR、越权、认证绕过差分、弱口令有效性——框架都无法自发复现，只能结构性降级为 INCONCLUSIVE。参照 bingo `requests.Session()`（cookie jar）+ `CredentialVerifier`（anti_hallucination.py:380），让框架**自己登录、自己持 cookie、自己确定性判定登录是否成功**，再用已认证会话去验证受保护资源。判定失败恒降级、绝不升级。

## 问题（已核对代码，非推测）

1. **无状态验证器**：`HTTPVerifier.request`（http_verifier.go:199）每次 `http.NewRequestWithContext` 都是裸请求，`client` 无 `Jar`（http_verifier.go:58-78）。跨请求不保持 cookie，无法表达"先登录拿会话，再带会话访问受保护端点"。
2. **无登录原语**：`FindingSpec`（http_verifier.go:17）只有单 payload/baseline 两次请求，没有"先 POST 登录"的前置步。IDOR/越权的核心差分是"**已认证 A 用户能读到 B 用户资源**"，当前验证器连"已认证"这一步都做不到。
3. **弱口令无法验证**：SRC 高频发现"默认口令 admin/admin 可登录"。当前无 `credential` 类型，无从判定登录真伪；模型自报的"登录成功"正是最易幻觉、最不可信的一类声明（controls 声明+脚本+判定的自指环）。
4. **bingo 的可借鉴与不可借鉴**：`CredentialVerifier.verify`（anti_hallucination.py:390-463）的**判定逻辑**值得忠实复刻——`meaningful_cookie`（排除 aspsessionid/phpsessid/jsessionid 泛型 cookie）+ success/fail 文本 + offsite/away-from-login 重定向三信号。但 `_guess_login_url` / `_default_login_template`（465-495）是站点特化硬编码（admin_id/login_proc.php），**违反本项目"无硬编码 phase"原则，不复刻**——登录端点与表单由模型在 FINDING 块声明，框架只负责执行与判定。

## 现有可复用基建

- `net/http/cookiejar`（标准库，符合仅标准库约束）：`cookiejar.New(nil)` 即得 RFC6265 jar。
- `HTTPVerifier`（http_verifier.go）：已有 scope 硬门、`maxBodyBytes` 截断、reproductions 重放、`VerifyWithEvidence` 返回 `(VerificationResult, VerificationRecord)`。登录步接在 scope 检查之后、payload 请求之前。
- `Score`（verification.go:69）+ `deterministicCheck`（verification.go:130）：确定性评分。新增 `credential` 分支即可复用整套权重/阈值/verdict 机制。
- `ParseFindingSpec`（finding_spec.go:40）：`key: value` 行解析器，加新字段即可。
- `verificationEvidence`（runner.go:76）落盘 + `RenderVerifiedFindings`（findings.go）渲染：登录结果顺此链路持久化与展示。

## 架构

```
VerifyWithEvidence(ctx, spec)                      [http_verifier.go]
  scope 检查 (payload host, 保持不变)
  ── 若 spec.LoginURL != "" ──
  │   verifyLogin(ctx, spec)                        [新]
  │     · 独立 cookie jar (cookiejar.New)
  │     · 跟随重定向, 每一跳 scope 强校验, 有跳数上限
  │     · 记录: 累计 cookie、首跳 Location、终态 status、响应片段
  │     · 确定性判定 login_verified (忠实 bingo 三信号)
  │   ── 登录失败 ──> credential 类型: 直接 REFUTED/INCONCLUSIVE (恒降级)
  │                  其它类型: 记 checkFailed "auth session not established", 继续无会话验证
  │   ── 登录成功 ──> 把 jar 的 Cookie 头注入 payload 请求 (baseline 不带, 形成差分)
  ├─ credential 类型: Evidence.LoginVerified 驱动 Score credential 分支
  └─ 其它类型: 已认证 payload vs 无认证 baseline 的既有差分评分
  返回 (result, record含login字段)

runner.persistVerificationEvidence                  [runner.go]
  verificationEvidence 增 login_* 字段 (脱敏: cookie 名 + 存在性, 不落原始口令/完整 cookie 值)

RenderVerifiedFindings                              [findings.go]
  credential 发现与 login 状态确定性渲染
```

## Global Constraints

- 模块名 `pentgo`；Go 1.25.0；**仅标准库**（`net/http/cookiejar` 属标准库）。
- 生产源码放 `internal/runtime|report/`；测试真身放 `tests/_packages/<same-rel-path>/`，源码目录建**相对符号链接**（现有 `http_verifier_test.go`、`runner_test.go`、`finding_spec_test.go` 已是符号链接，改断言改真身；新增测试文件须同步建符号链接）。
- 测试 package 与被测包同名，不用外部 `_test`。
- **无硬编码 phase / 无站点特化**：不复刻 bingo 的 `_guess_login_url`/`_default_login_template`。登录端点、方法、表单体全部由模型在 FINDING 块声明。
- **判定失败恒降级**：登录失败或会话未建立，只能降低或维持 verdict，绝不升级。沿用现有"验证失败→INCONCLUSIVE"纪律。
- **非幂等纪律**：登录 POST 是建立会话的必要一次性副作用，只发一次（不重放）；credential 类型的"payload"就是这次登录本身。已认证的 GET payload 仍按 `reproductions` 重放。
- **脱敏落盘**：`verificationEvidence` 与报告**不得记录原始口令明文与完整 cookie 值**；只记 cookie 名集合、`meaningful_cookie` 布尔、`login_verified` 布尔、status、响应片段（截断）。curl 复现命令中口令位以占位符或由模型 spec 原样带出（模型已知该口令），但落盘的结构化字段脱敏。
- **scope 强校验贯穿登录重定向**：登录跟随的每一跳都过 `scope.HostAllowed`，越界即停止跟随并记录。
- **不改现有判定权重/阈值**：`Score` 权重与 verdict 阈值不动；只新增 `credential` 分支与 `LoginVerified` 输入。
- 每步结束运行该步测试；每个 Task 至少一次提交。

---

### Task 1: `FindingSpec` 与 `VulnType` 扩展登录声明（TDD）

**Files:**
- Modify: `internal/runtime/verification.go`（加 `VulnCredential` 常量）
- Modify: `internal/runtime/http_verifier.go`（`FindingSpec` 加登录字段）
- Modify: `internal/runtime/finding_spec.go`（解析新字段 + `knownVulnType` 加 credential）
- Test: `tests/_packages/internal/runtime/finding_spec_test.go`（追加）

**Interfaces (Produces):**
- `const VulnCredential VulnType = "credential"`
- `FindingSpec` 新增字段：
  - `LoginURL string`（登录端点，空则不做登录步）
  - `LoginMethod string`（默认 POST）
  - `LoginBody string`（登录表单体，如 `username=admin&password=admin`）
  - `LoginContentType string`（默认 `application/x-www-form-urlencoded`）
  - `Username string`（仅用于报告标注，不参与判定）
- `parseFindingSpec` 解析新 key：`login_url`、`login_method`、`login_body`、`login_content_type`、`username`。
- `knownVulnType` 接受 `credential`。
- credential 类型放行条件：`VulnCredential` 且 `LoginURL != ""`（在 `ParseFindingSpecs` 里，credential 无 `LoginURL` 应被丢弃，因为没有 URL 兜底——注意现有过滤 `strings.TrimSpace(spec.URL) == ""` 对 credential 应改判为看 `LoginURL`）。

- [ ] **Step 1: Write failing tests**
  - 解析含 `type: credential` + `login_url:` + `login_body:` + `username:` 的块，断言字段落位、`VulnCredential` 被 `knownVulnType` 接受。
  - credential 块缺 `login_url` 时被 `ParseFindingSpecs` 丢弃。
  - 非 credential 块（xss）不受影响：无 login 字段仍正常解析。
- [ ] **Step 2: Run fails** — `go test ./internal/runtime -run "FindingSpec|Credential" -count=1`
- [ ] **Step 3: Implement** — 加常量、结构字段、解析分支；调整 `ParseFindingSpecs` 的空 URL 过滤：credential 看 `LoginURL`，其余看 `URL`。
- [ ] **Step 4: Run passes** + 整包回归 `go test ./internal/runtime -count=1`
- [ ] **Step 5: Commit** `feat: parse authenticated-login fields in finding specs`

---

### Task 2: `HTTPVerifier` 登录原语 + cookie jar（TDD）

**Files:**
- Modify: `internal/runtime/http_verifier.go`
- Test: `tests/_packages/internal/runtime/http_verifier_test.go`（追加）

**Interfaces (Produces):**
- `type loginOutcome struct { Attempted, Verified bool; StatusCode int; CookieNames []string; MeaningfulCookie bool; SuccessText, FailText, RedirectAway bool; Snippet string; SessionCookieHeader string; Error string }`
  - `SessionCookieHeader` 仅在进程内用于注入 payload 请求，**不落盘**（落盘用 `CookieNames`+`MeaningfulCookie`）。
- `func (verifier *HTTPVerifier) verifyLogin(ctx context.Context, spec FindingSpec) loginOutcome`
  - 建 `cookiejar.New(nil)`，用一个**跟随重定向、每跳 scope 强校验、跳数上限（如 10）**的临时 client（复用 `verifier.client` 的 Timeout，但 `CheckRedirect` 换成 scope 校验版）。
  - POST（或 `LoginMethod`）`LoginURL`，body=`LoginBody`，Content-Type=`LoginContentType`。
  - 判定忠实 bingo（anti_hallucination.py:437-448）：
    - `meaningfulCookie` = jar 中 cookie 名集合减去 `{aspsessionid, phpsessid, jsessionid, cfid, cftoken}` 非空。
    - `successText` = 响应含 `logout`/`log out`/`dashboard`/`welcome,`/`로그아웃`/`대시보드` 等（大小写不敏感）。
    - `failText` = 响应含 `incorrect`/`invalid`/`failed`/`wrong`/`틀렸`/`잘못된` 等。
    - `redirectAway` = 首个响应为 3xx 且 `Location` 不含 `login`。
    - `verified = !failText && (successText || redirectAway) && (meaningfulCookie || redirectAway)`。
  - 记录 `CookieNames`（脱敏用）、`SessionCookieHeader`（`k=v; k2=v2` 供注入）、`Snippet`（截 300 字节）。
- 常量：`loginRedirectLimit = 10`；泛型 cookie 集合、success/fail 词表定义为包级 `var`。

**Behavior:** scope 拒绝登录 host → `loginOutcome{Attempted:true, Error:"scope"}`，不发请求。登录请求 transport 失败 → `Verified:false, Error:...`，绝不升级。

- [ ] **Step 1: Write failing tests**（用 `httptest.NewServer`）
  - 成功登录：handler 对正确表单 set 一个非泛型 cookie（如 `sid=abc`）+ 返回含 `dashboard` 文本 → `verifyLogin` 返回 `Verified=true`、`MeaningfulCookie=true`、`SessionCookieHeader` 含 `sid=abc`。
  - 失败登录：handler 返回含 `invalid credentials` → `Verified=false`（即便 set 了泛型 cookie）。
  - 仅泛型 cookie（`PHPSESSID`）+ 无 success 文本 + 无重定向 → `Verified=false`（防 ASP/PHP 会话误判，忠实 bingo 注释）。
  - redirect-away：handler 302 到 `/dashboard`（Location 不含 login）→ `RedirectAway=true`、`Verified=true`。
  - scope 拒绝：`LoginURL` 指向 scope 外 host → `Error` 非空、未实际请求（可用未注册 host 或独立 scope 断言）。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — `verifyLogin` + cookie jar + scope 校验重定向 policy + 忠实判定。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: add framework login primitive with cookie jar`

---

### Task 3: 接入验证流 + credential 评分分支（TDD）

**Files:**
- Modify: `internal/runtime/http_verifier.go`（`VerifyWithEvidence` 编排登录步 + 注入会话）
- Modify: `internal/runtime/verification.go`（`Evidence` 加 `LoginVerified bool`；`deterministicCheck` 加 `VulnCredential` 分支）
- Test: `tests/_packages/internal/runtime/http_verifier_test.go`、`verification_test.go`（追加）

**Interfaces (Produces):**
- `Evidence` 新增：`LoginVerified bool`。
- `deterministicCheck` 新增 `case VulnCredential:` — `LoginVerified` 为真返回 `(true, "framework login verified")`，否则 `(false, "login not verified")`。credential 的 P5 deterministic 权重(0.4) 由登录真伪驱动；P2 causal（baseline vs response）对 credential 用"已认证响应 vs 未认证 baseline"，若未提供 baseline 则该项 checkFailed（正常，credential 主要靠 P5+P1+P4）。
- `VerifyWithEvidence` 编排：
  - payload host scope 检查（现状保留）后，若 `spec.LoginURL != ""` 调 `verifyLogin`。
  - **credential 类型**：payload 就是登录本身——`Evidence.LoginVerified = outcome.Verified`；`ResponseBody = outcome.Snippet`；`ReproductionCount` 记 1（登录不重放，非幂等纪律）；`Score` 走 credential 分支。登录失败 → confidence 低 → INCONCLUSIVE/REFUTED（恒降级）。
  - **其它类型 + 登录成功**：把 `outcome.SessionCookieHeader` 注入 payload 请求的 `Cookie` 头（不注入 baseline，形成"已认证 vs 匿名"差分）；其余流程不变。
  - **其它类型 + 登录失败/未建立**：`result.ChecksFailed` 追加 `"auth session not established"`，按无会话继续（保持既有行为，不升级）。
- `VerificationRecord` 增字段：`LoginAttempted, LoginVerified bool; LoginStatus int; LoginCookieNames []string; LoginMeaningfulCookie bool; LoginSnippet string`（**不含** `SessionCookieHeader` 原值、不含口令）。

**Behavior:** 会话 Cookie 头仅注入 payload，baseline 保持匿名。非幂等纪律：credential/登录请求发一次；已认证 GET payload 仍重放 `reproductions` 次（带同一 Cookie 头）。

- [ ] **Step 1: Write failing tests**
  - credential 端到端：登录成功的 httptest server → `VerifyWithEvidence(credential spec)` 得 `LoginVerified` 记录、verdict 至少 LIKELY（视权重）；登录失败 → INCONCLUSIVE/REFUTED。
  - 认证门控 IDOR：server 对带 `Cookie: sid=abc` 的 `/user/2` 返回他人数据，匿名返回 302/403 → 登录后 payload 带 cookie 命中差分，baseline 匿名，verdict 提升；断言 `record.LoginVerified=true` 且 payload 请求确实带了 Cookie 头（server 侧校验）。
  - 登录失败不升级：登录返回 invalid → 其它类型 spec 仍按匿名评分，`ChecksFailed` 含 auth 提示。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — 编排 + credential 分支 + record 字段。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: verify findings under an authenticated session`

---

### Task 4: 持久化登录证据 + 报告渲染（TDD）

**Files:**
- Modify: `internal/runtime/runner.go`（`verificationEvidence` 加 login_* 字段；`persistVerificationEvidence` 填充）
- Modify: `internal/report/findings.go`（`RenderVerifiedFindings` 渲染 credential/login 状态）
- Test: `tests/_packages/internal/runtime/runner_test.go`、`tests/_packages/internal/report/*_test.go`（追加）

**Interfaces (Produces):**
- `verificationEvidence` 新增（脱敏）：`login_attempted`、`login_verified`、`login_status`、`login_cookie_names []string`、`login_meaningful_cookie`、`login_snippet`。**不加**口令明文与完整 cookie 值。
- `persistVerificationEvidence` 从 `VerificationRecord` 的 login 字段填充。
- `RenderVerifiedFindings`：credential 发现在对应 verdict 小节额外渲染 `- Login verified: true/false`、`- Session cookies: sid, ...`（名字，不含值）、`- Username: <spec.Username>`（若有）。

- [ ] **Step 1: Write failing tests**
  - 假 verifier 返回带 login 字段的 record → `ConsolidateAndVerify` 后假 sink 收到的 `verification-001` value 含 `login_verified`，且**不含**原始口令/完整 cookie 值（断言序列化 JSON 不含口令字符串）。
  - `RenderVerifiedFindings` 输入一条 credential VERIFIED（login_verified=true）→ 输出含 `Login verified: true` 与 cookie 名，不含 cookie 值。
- [ ] **Step 2: Run fails**
- [ ] **Step 3: Implement** — evidence 字段 + 填充 + 渲染。
- [ ] **Step 4: Run passes** + 整包回归
- [ ] **Step 5: Commit** `feat: persist and render authenticated login evidence`

---

### Task 5: 更新 consolidation 提示词 + 全量验证

**Files:**
- Modify: `internal/runtime/runner.go`（`findingConsolidationSystemPrompt` 增 credential 类型与登录字段说明）
- Modify: `tests/_packages/internal/runtime/*`（若提示词断言存在则同步）

**Interfaces (Produces):**
- consolidation 提示词补充：支持 `type: credential`；可选字段 `login_url`、`login_method`、`login_body`、`login_content_type`、`username`；说明"需要已认证身份才能触发的发现（IDOR/越权/认证绕过）应在块内提供 login_url + login_body，框架将自行登录并携带会话验证"；强调仅声明**已在执行证据中观察到**的登录端点与凭据，不得臆造。

- [ ] **Step 1**: 更新提示词常量；`supported types` 列表加 `credential`。
- [ ] **Step 2: 全量回归**
  ```bash
  go build ./...
  go test ./... -timeout 120s
  go vet ./...
  git diff --check
  ```
- [ ] **Step 3: 端对端冒烟（授权目标）**：对 `https://lycvc.linyi.cn/` 或本地 httptest 复现，确认：
  - credential 发现产出 `login_verified` 落盘于 `evidence/verification-*.json`（脱敏，无明文口令）；
  - 认证门控发现的 payload 请求确实携带会话、baseline 匿名；
  - `report.md` 的"已验证发现"渲染 login 状态；
  - 登录失败样例恒降级为 INCONCLUSIVE/REFUTED。
  - 纪律：仅对授权 scope 发登录请求；不绕过安全分类器；非幂等登录只发一次。
- [ ] **Step 4: Commit** `feat: instruct consolidation to declare authenticated findings`

---

## 自查

- **补上认证断裂**：框架自持 cookie jar、自行登录、自判登录真伪（Task 2+3），IDOR/越权可在已认证 vs 匿名差分下验证，弱口令有效性可确定性判定。
- **忠实 bingo 判定、不抄硬编码**：复刻 `meaningful_cookie`+success/fail 文本+redirect-away 三信号（anti_hallucination.py:437-448）；登录端点/表单由模型声明，不复刻 `_guess_login_url`/`_default_login_template` 硬编码。
- **恒降级**：登录失败只降不升；credential 靠 P5(登录真伪) 驱动，未验证即 INCONCLUSIVE/REFUTED。
- **脱敏**：落盘/报告只留 cookie 名+存在性+status+片段，无明文口令、无完整 cookie 值（Task 4 断言覆盖）。
- **scope 贯穿**：payload host 与登录每一跳重定向都过 `scope.HostAllowed`。
- **非幂等纪律**：登录 POST 一次；已认证 GET payload 仍重放。
- **不改判定权重/阈值**：`Score` 权重/verdict 阈值不动，只加 credential 分支与 `LoginVerified` 输入。
- **类型一致性**：`VulnCredential`(T1) ↔ `deterministicCheck` 分支(T3) ↔ `knownVulnType`(T1) ↔ consolidation 提示词(T5)；`loginOutcome`(T2) → `VerificationRecord` login 字段(T3) → `verificationEvidence`(T4) → `RenderVerifiedFindings`(T4) 字段贯通。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接；改断言改真身，新增测试文件须建符号链接（现有 `http_verifier_test.go`/`runner_test.go`/`finding_spec_test.go` 已是链接）。
2. `net/http/cookiejar` 为标准库，满足仅标准库约束。
3. Task 顺序有依赖：T1(spec 字段)→T2(登录原语)→T3(编排+评分，依赖 T1/T2)→T4(落盘+渲染，依赖 T3 record 字段)→T5(提示词+全量)。逐个提交。
4. `SessionCookieHeader` 仅进程内注入 payload 请求，严禁进入 `VerificationRecord`/`verificationEvidence`/报告。
5. credential 的空 URL 过滤改为看 `LoginURL`（T1），勿沿用 `spec.URL` 兜底逻辑误杀 credential 声明。
6. 若 consolidation 提示词有测试断言（prompt_content_test.go 等），T5 同步。
