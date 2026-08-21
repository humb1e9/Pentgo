# Engagement Session Pool 实施计划（复刻 bingo 下一刀）

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development 或 executing-plans。  
> 前置：`2026-07-18-authenticated-session-verification.md` + `2026-07-19-idor-dual-session-verification.md` 已完成。

**Goal:** 把 bingo 真正拉开差距的能力补上——**整次 engagement 内可复用的已认证会话池**，而不是只在 consolidate 阶段临时登录。模型代码块与框架验证共用同一身份真相源；会话由框架建立/判定/脱敏，失败恒降级。

---

## 第一性原理：bingo 到底强在哪

| 层 | bingo | PentGo 现状 | 杠杆 |
|----|-------|-------------|------|
| TianTi 5 原则 Score | `VerificationEngine` | `verify.Score` | 已齐 |
| 登录三信号 | `CredentialVerifier` | `verifyLogin` | 已齐 |
| 双会话 IDOR 差分 | `IdorScanner(headers_a,b)` | `VulnIDOR` + `ResponseDiffers` | 已齐 |
| **整次任务会话池** | `SessionManager` + `SessionPool` + CSRF | **无**（每次 FINDING 重登；模型块冷启动） | **最高** |
| 工具侧带 cookie | `T.set_cookie` / agent 持 jar | 模型每次手写 Cookie | 随会话池解决 |
| Fatigue / pivot | `FatigueMonitor` | 仅 max_turns + 7-gate 文案 | 中 |
| 90+ 硬编码 scanner | tools/* | skills + 模型写代码 | **故意不做** |
| phase 流水线 | recon→scan→exploit | 无 phase | **故意不做** |

结论：再抄一个 scanner 或加 phase **不是**复刻 bingo。  
bingo 的实质是：**框架持有身份与差分证据；模型只负责发现与声明。**  
下一刀补「身份在整次 engagement 内连续存在」——对应 `bingo/tools/session_manager.py`（`SessionPool` / CSRF / ensure_logged_in），**不**抄 `LoginExecutor._detect_form` 的站点硬编码猜路径。

---

## 问题（当前断裂）

1. **验证侧有会话、探索侧没有**  
   `HTTPVerifier.verifyLogin` 只在 `ConsolidateAndVerify` 时为单条 FINDING 建 jar，用完即弃。模型在 loop 里跑的 Python/Bash **不继承**任何 cookie。

2. **每条 FINDING 重复登录**  
   IDOR 要 A/B 各登一次；同一 engagement 多条 `credential`/`idor`/`auth_bypass` 会 N 次非幂等 POST。bingo 是 pool 命中后复用。

3. **模型无法可靠「继续当已登录用户」**  
   只能 stdout 声称登录成功，或每块重复登录脚本——违反 anti-hallucination（身份真相应框架持有）。

4. **无 CSRF 预取**  
   bingo `SessionManager.login` 先 GET 抽 `_token`/`csrf-token` 再 POST。PentGo `verifyLogin` 直接 POST `login_body`，对现代表单脆弱。

---

## 非目标

- 不引入 multi-agent / phase 调度（`redteam/pipeline.py`）。
- 不移植 nuclei / 站点硬编码工具树。
- 不把 cookie 值写入 evidence JSON、报告、timeline。
- 不猜登录 URL（仍由模型证据声明）。
- 不做垂直越权专用 scanner（后续可在 pool 上加 `role=admin` 差分，本计划只建池）。

---

## 设计

### 1. Engagement 级 `SessionPool`（新，标准库）

包位置（避免 import 环）：

```
internal/runtime/session/pool.go   # AuthSession + SessionPool
```

`loop` 已依赖 `session`；`verify` 不得依赖 `loop`。  
→ **pool 放在 `session` 包**；`verify` 通过接口或函数参数接收「按 name 取 Cookie header」，不反向依赖。

```go
// AuthSession is a framework-owned login identity for one engagement.
// Cookie values stay in memory only; persistence stores names + metadata.
type AuthSession struct {
    Name               string
    Role               string // user | admin | other (opaque)
    Username           string
    LoginURL           string
    CookieHeader       string   // "a=b; c=d" for HTTP Cookie
    CookieNames        []string
    MeaningfulCookie   bool
    LoginStatus        int
    Verified           bool
    CSRFToken          string   // optional last extracted
    EstablishedAt      time.Time
}

type SessionPool struct {
    mu   sync.Mutex
    byName map[string]*AuthSession
}
```

API（最小集，对齐 bingo）：

| 方法 | 行为 |
|------|------|
| `Put(s *AuthSession)` | 覆盖同名 |
| `Get(name string) (*AuthSession, bool)` | 仅 Verified 可选用 |
| `GetByRole(role string)` | 第一个 Verified 匹配 |
| `Names() []string` | 供 prompt 注入清单 |
| `ExportCookieEnv() map[string]string` | 见下 |
| `PublicView() []SessionPublic` | 落盘/报告：无 cookie 值、无 password |

### 2. 建立会话的入口（两路，同一真相源）

**A. 显式 SESSION 块（探索期）** — 模型在 loop 中声明：

```text
=== PENTGO SESSION ===
name: user_a
role: user
username: alice
login_url: https://t/login
login_method: POST
login_body: username=alice&password=secret
login_content_type: application/x-www-form-urlencoded
=== END PENTGO SESSION ===
```

Runner 每 turn 解析 → 调 `verify.EstablishSession`（见下）→ `pool.Put`。  
失败：timeline `session_failed`，**不**注入 env，**不**升级任何 finding。

**B. FINDING 登录回写（验证期）** — `VerifyWithEvidence` 成功 `verifyLogin` / `verifyLoginB` 后：

- 若 `spec.Username` / `UsernameB` 非空，以 `username` 或 `user_a`/`user_b` 为 name 写入 pool（已存在且 Verified 则复用，跳过重登）。
- `login_url_b` 路径同理。

### 3. `verify.EstablishSession` + CSRF 预取

从现有 `verifyLogin` 抽出/增强：

```
EstablishSession(ctx, loginSpec) (AuthSession-like result, error)
```

步骤（忠实 bingo `SessionManager.login` 骨架）：

1. **可选 GET** `login_url`（scope 校验）→ `CsrfExtractor` 抽 token。  
2. 若抽到 token 且 body 尚未含常见 csrf 键：合并进 form body（`_token` / `csrf_token` / `authenticity_token` 其一有值即可；**不**站点硬编码字段名列表过长——对齐 bingo 3–4 个模式）。  
3. 现有 `verifyLogin` 三信号判定。  
4. 返回 `CookieHeader` + names + Verified。

`CsrfExtractor`：标准库 `regexp`，模式对齐 bingo `session_manager.CsrfExtractor.PATTERNS`。

### 4. 注入模型执行环境

`exec.Executor` 已有：

```go
command.Env = append(os.Environ(),
  "PENTGO_TARGET="+...,
  "PENTGO_ENGAGEMENT_ID="+...,
  "PENTGO_WORKDIR="+...,
)
```

扩展 `ExecutionInput`（或 ExecutorConfig 上的 `EnvExtra map[string]string`）：

| 环境变量 | 含义 |
|----------|------|
| `PENTGO_SESSIONS` | 逗号分隔已验证 name 列表，如 `user_a,user_b` |
| `PENTGO_SESSION_<NAME>_COOKIE` | 该身份完整 Cookie header（仅子进程可见） |
| `PENTGO_SESSION_<NAME>_USER` | username |
| `PENTGO_SESSION_<NAME>_ROLE` | role |

约束：

- NAME 规范化：`[A-Za-z0-9_]+`，非法名丢弃。  
- evidence / stdout 落盘路径**不**自动 echo 这些变量；prompt 要求模型**禁止** `print(os.environ[...COOKIE...])`。  
- 报告与 `verificationEvidence` 仍只记 cookie **名**。

### 5. FINDING 复用 pool（少登录）

`VerifyWithEvidence` 流程调整：

```
if spec.LoginURL != "" {
  if cached := pool.Get(sessionName(spec)); cached.Verified {
    use cached CookieHeader  // 跳过 POST login
  } else {
    login → pool.Put
  }
}
// B 同理
```

`sessionName`：优先 `spec.SessionName`（新可选字段）→ 否则 `spec.Username` → 否则 `"default"`。  
双会话：`SessionNameB` / `UsernameB` / `"default_b"`。

### 6. Prompt 与 Skill 轻触

`loop/prompt.go` 增加短段落（无 phase）：

- 登录成功后应发 `PENTGO SESSION` 块，由框架持有会话。  
- 后续代码用 `os.environ["PENTGO_SESSION_<name>_COOKIE"]` 设请求头。  
- 列表 `PENTGO_SESSIONS` 表示当前可用身份。

`skills/idor-...` / recon：一句「双用户先 SESSION 再测对象 URL」即可（可选，非阻塞）。

### 7. 落盘与 AgentSession

`AgentSession` 增加：

```go
Sessions []SessionPublic `json:"sessions,omitempty"`
```

`SessionPublic`：`name, role, username, login_url, cookie_names, verified, established_at`——**无** cookie 值、**无** password。

`PublishWithReport` 写入 session.json 时带上 PublicView。

---

## 架构落点

```
loop.Runner
  ├── SessionPool (engagement 生命周期)
  ├── 每 turn: parse SESSION 块 → verify.EstablishSession → pool.Put
  ├── exec.Execute(..., EnvExtra from pool.ExportCookieEnv())
  └── ConsolidateAndVerify → HTTPVerifier.VerifyWithEvidence(pool)
        └── 命中 pool 跳过重复登录；成功登录回写 pool
```

依赖方向保持：

```
exec → authz → verify → session → loop
```

`verify` 新增可选接口：

```go
type SessionSource interface {
    CookieHeader(name string) (header string, ok bool)
    Remember(name string, header string, meta ...) // 或由 loop 在 verify 后 Put
}
```

更简实现：**loop 持 pool**；调用 `verifier.VerifyWithEvidence` 前把 A/B cookie 填进临时字段 / 扩展 `FindingSpec` 运行时覆盖——避免 verify→session 环。  
推荐：扩展 `HTTPVerifier` 方法

```go
func (v *HTTPVerifier) VerifyWithEvidence(ctx, spec FindingSpec, opts VerifyOptions) ...
type VerifyOptions struct {
    CookieA, CookieB string // 非空则跳过对应 login
    OnLoginA, OnLoginB func(loginResult) // 回写 pool
}
```

---

## 任务拆分（TDD）

### T1 — `session.SessionPool` + PublicView

- 文件：`internal/runtime/session/pool.go` + `pool_test.go`
- 测试：Put/Get、未 Verified 不可用、ExportCookieEnv 键名规范、PublicView 无 cookie 值
- Commit: `feat: add engagement session pool`

### T2 — CSRF 预取 + `EstablishSession`

- 文件：`internal/runtime/verify/csrf.go`、`http_verifier.go`（重构 `verifyLogin` 共用）
- 测试 httptest：
  - GET 含 `<input name="csrf_token" value="tok">`，POST 必须带 token 才 set `sid`
  - 无 CSRF 页面仍可登录（回归）
  - 失败登录不 Verified
- Commit: `feat: csrf prefetch on framework login`

### T3 — 解析 `PENTGO SESSION` + Runner 接线

- 文件：`loop/session_block.go`（parse）、`runner.go` 每 turn 处理
- 测试：模型输出 SESSION 块 → pool 有 Verified 会话；失败不污染 pool
- Commit: `feat: establish sessions from model SESSION blocks`

### T4 — Executor 注入 cookie env

- 文件：`exec/executor.go`、`ExecutionInput.ExtraEnv`
- 测试：子进程 `print(os.environ.get("PENTGO_SESSION_user_a_COOKIE"))` 得到框架注入值；evidence JSON **不含**该值（若 sink 序列化 env 则显式 redact）
- Commit: `feat: inject session cookies into block env`

### T5 — Verify 复用 pool + 回写

- `VerifyWithEvidence` + opts / 预填 cookie
- 测试：同一 httptest 登录计数器 = 1 时连续两条 FINDING 共用 cookie；IDOR A/B 从 pool 取
- Commit: `feat: reuse engagement sessions in HTTP verification`

### T6 — session.json PublicView + prompt 段落 + 全量测试

- `AgentSession.Sessions`、prompt 短文、`go test ./...`
- Commit: `feat: surface session pool in reports and system prompt`

---

## 全局约束（沿用）

- 仅标准库；测试跟包走。  
- 非幂等：每个**新**身份登录一次；pool 命中不重复 POST。  
- scope 校验 login_url 与后续资源 URL。  
- 失败恒降级；cookie 值永不进 evidence/report。  
- 无 phase 硬编码、无猜登录路径。  
- AGENTS.md：网络行为以本地 httptest fixture 验证。

---

## 刻意后置（本计划之后）

| 项 | 为何后置 |
|----|----------|
| `FatigueMonitor` 式 pivot 注入 user 消息 | 会话连续后更有意义；否则只是催模型换 payload |
| 垂直越权 `role=user` vs admin 资源 | 依赖 pool 的 role 字段，本计划只建字段不写 Score 分支 |
| ensure_logged_in 过期重登 | 先有 pool；过期检测可第二迭代（bingo EXPIRY_PATTERNS） |
| SSRF/SSTI/XXE 确定性 P5 | 类型扩展与会话正交，可并行但非「复刻 bingo 身份」主线 |

---

## 自查清单

- [ ] 模型块能读到框架注入的 Cookie，且报告无 cookie 值  
- [ ] SESSION 登录失败 → 无 env 注入、无 finding 升级  
- [ ] 同 username 两条 FINDING 只触发一次登录 POST（httptest 计数）  
- [ ] CSRF 必需站点：无 token 登录失败；有 token 成功  
- [ ] IDOR 仍可声明 login_*_b；也可只声明 SESSION 名 + url  
- [ ] `go test ./...` 全绿  
- [ ] 无 phase、无站点硬编码路径  

---

## 成功标准（复刻 bingo 的可观测定义）

1. **身份真相源唯一**：pool + verify，不靠模型 stdout「我已登录」。  
2. **探索与验证同会话**：loop 内代码与 consolidate 验证可对同一 `user_a` cookie。  
3. **差分仍框架字节**：IDOR/auth 继续 `ResponseDiffers` / Score，不回退模型自证。  
4. **不膨胀工具树**：0 个新 CVE scanner；能力落在 runtime 身份层。

---

## 建议执行顺序

1. 写本计划（本文）→ 用户确认  
2. Codex/agent 按 T1→T6 实施  
3. Claude 跑 `go test ./...` + 本地双用户 httptest engagement 冒烟（不碰外网）  
4. 再开疲劳 pivot / 垂直越权 Score 计划
