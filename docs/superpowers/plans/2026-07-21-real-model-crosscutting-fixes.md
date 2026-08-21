# 三处横切修复执行计划（交 codex，按 1→2→3 顺序）

## Context

真实模型（deepseek-v4-flash）两次本地 httptest 端到端验收暴露：privesc/idor/credential 等框架验证能力单测全绿、脚本化 engagement 通过，但真实模型跑不通或框架空转。根因是三处横切问题，与具体漏洞类型无关：

1. **preflight 破坏真实模型代码**：`addRequestsTimeout` 注入 `timeout=15` 会破坏 `requests.Session()`、链式 `.json()`、多行调用，导致 Python 反复被拒/失败，模型被迫改 curl，烧回合。
2. **consolidation 静默空转**：模型在 consolidation 阶段用 prose 自证漏洞而非产出 `PENTGO FINDING` 块时，`ParseFindingSpecs` 返回空，框架验证从不运行，0 发现且无重试无报错。
3. **IDOR oracle 语义弱**：双会话 IDOR 判"两响应相异"即漏洞，但两个不同用户数据本就相异（正常行为），且单会话也能到 VERIFIED。真正的水平 IDOR 信号是"攻击者 A 看到属主 B 的身份数据"（相似）。

约束（AGENTS.md）：仅 CTF；网络行为只用本地 httptest 验证；cookie/密码永不进 evidence/report/history；不硬编码目标路径；框架拥有验证。保持既有绿测，除非语义必须改（下文逐一点名）。

---

## FIX 1 — preflight timeout 注入不再破坏 requests 代码

**文件**：`internal/runtime/exec/preflight.go`

**改点**：重写 `addRequestsTimeout`（137-156 行）；`repairPython`（97-119 行）的 import 前置改为在 shebang/docstring 之后插入（次要，可选）。

**算法**（`addRequestsTimeout`）：
```
HTTP_METHODS = get|post|put|delete|patch|head|options|request
逐行处理，保留换行：
  line 不含 "requests." → 原样
  line 含 "timeout=" → 原样（已有）
  正则匹配 requests.(HTTP_METHODS)\s*\(  → 无匹配则原样（Session()/session()/其他安全跳过）
  从该方法调用左括号后开始做括号配平扫描，找到属于该调用的匹配右括号：
    找不到（本行无 ) → 多行调用）→ 原样跳过，绝不破坏
  在该匹配右括号前插入 "timeout=15"（括号内已有参数用 ", " 分隔，空括号用 ""）
```
括号配平确保 `requests.get(url).json()` 只作用于 `get(...)` 的括号、不碰 `.json()`；`requests.Session()` 因方法名不在集合内被跳过；多行调用因本行无匹配右括号被安全跳过（timeout 宁可不加也不破坏）。`compilePython`（158 行）已在修复后语法校验并拒绝——确认当前症状正是"被破坏代码遭 preflight 拒绝"。

**测试**（`preflight_test.go`）：
- 保留 `TestPreflightRepairsMissingImportsAndHTTPTimeout`（30 行）：单行 `requests.get(parsed.geturl())` 仍得 `timeout=15` 且 3 条 repairs。
- 新增：`Session()` 不变；`requests.get(url).json()` → `requests.get(url, timeout=15).json()`；多行 `requests.get(\n url\n)` 不变（安全跳过）；`requests.post(url, data=x)` → 追加 `, timeout=15`。

**验证**：`go test ./internal/runtime/exec -run TestPreflight -count=1 && go vet ./internal/runtime/exec`

---

## FIX 2 — ConsolidateAndVerify 空结果重试一次

**文件**：`internal/runtime/loop/runner.go`

**改点**：`ConsolidateAndVerify`（413-448 行），在 `ParseFindingSpecs`（425 行）后加一次重试。

**算法**：
```
specs = ParseFindingSpecs(response.Content)
if len(specs) == 0:
  history.Append("user", 固定纠偏文本:"CONSOLIDATION CORRECTION: 未检测到 === PENTGO FINDING === 块。
     必须只输出结构化 finding 块,prose 会被忽略;无证据则不输出。")
  response = runner.chat(...)                # 复用同一 consolidation system prompt
  if err: 记录 verification_consolidation_error, return nil
  specs = ParseFindingSpecs(response.Content)
  if len(specs) == 0:
    session.AddEvent(turn,"verification_consolidation_empty","模型两次未产出可解析 finding 块", now)
    return nil
# 其余（MaxFindings 截断、findingSpecs 赋值、逐条 verifyFinding+持久化）保持不变
```
只重试一次，避免烧回合/token。纠偏文本语气对齐 Run 循环里既有 recovery 消息。

**测试**（`runner_test.go`，复用 `scriptedClient`/`recordingFindingVerifier`）：
- prose→有效 finding 块：得 1 条已验证发现，2 次 chat，第二次请求含纠偏消息。
- 连续两次 prose：0 发现 + timeline `verification_consolidation_empty`，不崩溃。

**验证**：`go test ./internal/runtime/loop -run 'Consolidate' -count=1 && go vet ./internal/runtime/loop`

---

## FIX 3 — IDOR oracle 改为双会话"身份泄漏"（相似）语义

**文件**：`internal/runtime/verify/verification.go`（+ 两个测试文件）。`ResponseDiffers`（258-301 行）保留给单会话弱路径。无需改 `finding_spec.go`。

**改点**：`deterministicCheck` 的 `VulnIDOR` 分支（204-219 行）。

**算法**：
```
VulnIDOR:
  statusCode ∉ {200,201,206} → false "idor payload status not accessible"
  if DualLoginVerified:                       # 水平 IDOR = 相似信号
     shared,reason = SharedIdentityFields(ResponseBody=A视图, BaselineBody=B属主视图)
     shared → true  "dual-session idor identity leak: "+reason
     else   → false "dual-session no shared identity: "+reason
  else:
     # 单会话/匿名：弱信号,不授予 P5 → 最高只能 LIKELY,永不单独 VERIFIED
     return false, "idor single-session not deterministic (needs dual-session identity overlap)"

SharedIdentityFields(a,b):                     # 镜像 PrivilegedContentLeaked 的 JSON 身份重叠
  两者 tryJSONMap 均成功,否则 false "non_json_bodies"
  for k in [id,user_id,email,username,name,uid,userId]:
     两边都有该键且值相等 → true "shared_identity_"+k+": "+v
  false "no_shared_identity_fields"
```
单会话不授予 P5（返回 false）→ 依赖 P1/P2/P3/P4，置信度上限 0.6 = LIKELY，永不单独 VERIFIED，与 credential/open_redirect 封顶方式一致。这是对 Plan 初稿的修正（初稿单会话仍 `return true`，未真正封顶）。

**已知限制**：双会话 IDOR 现要求两响应均为 JSON。非 JSON（HTML）IDOR 在双会话下不判 VERIFIED——低误报优先，先接受，注释标记；后续可仿 privesc 的 `sharedPrivilegedToken` 补非 JSON 子串重叠。

**测试改动（语义必须改，逐一说明）**：
- `verification_test.go:195` `TestScoreIDORDualSessionDiff`：现 fixture owner=`id:1/alice`、other=`id:2/bob`**不共享身份**——正是审计指的误报场景,新 oracle 下**应不再 VERIFIED**。改为共享身份正例：两视图都含 `"id":2,"username":"bob","email":"bob@ex.test"`,仅私有字段（note）不同 → VERIFIED。
- 新增 `TestScoreIDORDualSessionNoSharedIdentityNotVerified`：A=`id:1/alice`、B=`id:2/bob`（合法不同用户,无共享身份）→ 不 VERIFIED（锁死 anti-false-positive）。
- 新增 `TestScoreIDORSingleSessionCannotVerify`：单会话 + ResponseDiffers=true → 置信度 <0.75，最高 LIKELY。
- `http_verifier_test.go:514` `TestVerifyWithEvidenceDualSessionIDOR`：现 fixture 两视图**已共享** `id:2/userB/email`（仅 secret 不同）→ 新 oracle 下天然通过,**无需改 fixture**;仅确认断言仍成立。
- `http_verifier_test.go:585` `TestVerifyWithEvidenceIDORNoDiffRefutes`、`verification_test.go:217` `TestScoreIDORNoDiffIsNotVerified`：保持不变（相同 body 仍拒绝）。

**验证**：`go test ./internal/runtime/verify -run IDOR -count=1 && go test ./internal/runtime/verify -count=1`

**回归风险**：FIX 3 只改 `VulnIDOR` 分支与新增 `SharedIdentityFields`,不碰 xss/sqli/rce/auth_bypass/upload/open_redirect/credential/privesc 的打分路径；privesc 复用的 `tryJSONMap` 不改签名。

---

## 组合最终验证

```bash
cd /home/kali/PentGo
go test ./... -count=1
go vet ./...
go build ./...
go mod verify
```
- 回归确认 privesc 及其余 8 类型打分测试不变。
- **真实模型本地 httptest 验收**（人工，仅本机）：脆弱应用(双登录端点、低/高权身份、共享身份 JSON)，真实模型探索取证后 consolidation 声明 idor/privesc finding；确认 FIX 2 在模型 prose 自证时触发一次纠偏、FIX 1 让 `requests.Session()` 探索代码不再被拒、FIX 3 对合法不同用户不误报。evidence/report 无 cookie/密码。

## 关键文件
- internal/runtime/exec/preflight.go（+ preflight_test.go）
- internal/runtime/loop/runner.go（+ runner_test.go）
- internal/runtime/verify/verification.go（+ verification_test.go、http_verifier_test.go）

## 顺序理由
1 先修（每次 engagement 都踩，解锁真实模型探索代码）→ 2（让模型产出的 finding 真正进入框架验证）→ 3（保证进入验证的 IDOR 判定低误报）。三者叠加才让已实现的认证类验证能力对真实模型真正可用。
