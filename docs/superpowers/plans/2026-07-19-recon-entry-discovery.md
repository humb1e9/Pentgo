# Recon 入口发现（SRC 导向）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 解决「只会戳操作者给的主站 URL」的产品缺口。在**不硬编码 phase** 的前提下，让模型在授权范围内系统化地：发现相关主机/入口 → 识别登录/API/管理面 → 用可回灌证据记录资产图 → 再进入漏洞探测与框架验证。优先改 **Skill + 系统提示**；框架侧只补「scope 感知的发现纪律」与可测断言，不引入固定扫描流水线。

**Depends on:** 认证会话已落地（`verify` cookie jar / credential）；`AGENTS.md` CTF/fixture 纪律——网络向行为以本地 `httptest`/fixture 与单元测试验证，不默认对外部站做破坏性扫描。

## 问题（已核对）

1. **`skills/recon/SKILL.md` 仅 7 行**，无入口发现方法论、无资产图输出约定、无「主站无登录则扩展同 scope 入口」策略。
2. **`baseSystemPrompt`**（`loop/prompt.go`）强调证据与 7-gate，但**没有** recon 优先序：未要求先建资产图、先找 auth 面再深测。
3. **Scope 是硬门**（`authz`）：模型可声明任意 host，越权被拦。Skill 必须教「只在 `PENTGO_TARGET` 主机 + 配置 `allowed_hosts` 内扩展」，禁止默认全网子域爆破。
4. **与下一计划衔接**：入口发现产出的 `login_url`/表单证据，是 `credential` / 已认证 FINDING 的唯一合法来源（consolidation 已要求不得臆造登录）。

## 非目标

- 不引入硬编码 phase 列表或固定「recon→scan→exploit」状态机。
- 不内置 subfinder/nuclei 二进制依赖；模型用 Python/Bash + 标准工具（若环境有）自写。
- 不把 bingo 的 site-specific login path 硬编码进框架。
- 本计划**不做** IDOR 双会话验证器（见 `2026-07-19-idor-dual-session-verification.md`）。

## 设计原则

| 原则 | 落地 |
|------|------|
| 决策层 = 提示/Skill | recon 方法论、资产图格式、何时 SKILL_LOAD |
| 执行层 = 硬门 | `authz` 继续拦越权 host / 破坏性写 |
| 证据唯一 | 只有 stdout/stderr/evidence 可支撑「发现了登录面」 |
| 可测 | prompt/skill 关键短语有测试；本地 fixture 模拟多路径首页 |

## 资产图约定（模型输出，非框架 schema）

Skill 要求模型在 early turns 打印**机器可读**资产摘要（便于后续轮次与 consolidation），示例：

```text
=== PENTGO ASSET MAP ===
target: https://example.test
hosts_in_scope:
  - example.test
entries:
  - kind: login
    method: GET
    url: https://example.test/login
    evidence: form fields username,password
  - kind: api
    url: https://example.test/api/v1/
    evidence: status 401 www-authenticate
  - kind: admin
    url: https://example.test/admin/
    evidence: status 302 location=/login
notes: no login on / ; found via /login link on homepage
=== END PENTGO ASSET MAP ===
```

框架**不解析**该块做自动验证（避免硬编码 phase）；仅作为回灌证据与 skill 契约。后续 IDOR 计划可选择解析，本计划不要求。

## 任务

### Task 1: 重写 `skills/recon/SKILL.md`（方法论 + 资产图）

**Files:**
- Modify: `skills/recon/SKILL.md`
- Modify: `skills/registry.go`（`descriptions["recon"]` 一句描述更新）
- Test: `skills/registry_test.go` 若有 catalog 断言则同步；新增或扩展 `internal/runtime/loop/prompt_*` 不强制测 skill 全文

**Skill 必须覆盖（中文，可执行）：**

1. **范围纪律**  
   - 仅 `PENTGO_TARGET` 主机 + 操作者 intent 中明确允许的 host；越权由 runtime 拦截，模型不得「赌」。  
   - 子域/旁站：**仅当** intent 或配置允许（例如写明 `*.example.com` / allowed_hosts）；否则只做**同 host** 路径与链接发现。

2. **优先序（建议，非强制 phase）**  
   - 首页 + 响应头/指纹 → 同页链接与 form → 常见入口路径轻量探测（login/admin/api/oauth/sso，**限流**）→ 记录 401/302/表单 → **有登录面再** SKILL_LOAD 认证类 skill。  
   - 无登录面：记入 asset map，转入反射/注入等公开面测试，**不要**臆造 credential FINDING。

3. **入口信号**  
   - HTML: `password` input、login/signin 文案、form action  
   - HTTP: 401/403、`WWW-Authenticate`、302→login、Set-Cookie 会话名  
   - API: `/api`、`/graphql`、JWT 提示、OpenAPI 路径

4. **速率与非破坏**  
   - 路径枚举小词表、间隔、不爆破密码、不写数据。

5. **输出**  
   - 每个代码块打印可判断的 JSON/文本证据；适时打印 `PENTGO ASSET MAP` 块。

6. **与验证衔接**  
   - 观察到的登录表单字段/URL 必须出现在后续执行证据中，才能在 consolidation 声明 `login_url`/`login_body`。

- [ ] **Step 1:** 写满 SKILL.md（建议 80–200 行，结构清晰，无悬空外链）。  
- [ ] **Step 2:** 更新 `descriptions["recon"]` 为更具体的一句话（含「入口/资产图/scope」）。  
- [ ] **Step 3:** `go test ./skills -count=1`  
- [ ] **Step 4:** Commit `docs(skills): expand recon for entry and asset-map discovery`

### Task 2: 系统提示增加 recon 导向（非 phase）

**Files:**
- Modify: `internal/runtime/loop/prompt.go`（`baseSystemPrompt`）
- Test: `internal/runtime/loop/prompt_content_test.go` / `prompt_test.go`

**在 HOW YOU WORK 或新小节增加（英文，与现提示一致）：**

- Early engagement: prefer loading `recon` when the task is broad assessment or target surface is unknown.  
- Build evidence of **in-scope entry points** (especially auth) before deep exploit claims.  
- Stay within authorized hosts; runtime blocks out-of-scope.  
- Do not invent login endpoints or credentials not present in returned output.  
- Print structured observations; completion still requires 7-gate + real evidence.

**不要**写死「必须先跑 N 轮 recon」——保持 agent 循环自由度。

- [ ] **Step 1:** 失败测试：断言 `basePromptContent()` 含关键短语（如 `recon`、`entry`/`login`、`in-scope`、不得臆造登录）。  
- [ ] **Step 2:** 改 `baseSystemPrompt`。  
- [ ] **Step 3:** `go test ./internal/runtime/loop -run Prompt -count=1` + 全包回归。  
- [ ] **Step 4:** Commit `feat: steer agent toward in-scope entry discovery`

### Task 3: 本地 fixture 冒烟（可选但推荐，CTF 纪律）

**Files:**
- 不进主库亦可：`docs` 或 `/tmp` 脚本说明；若加测试，用 `httptest` 在 `loop` 或 `app` 集成测「模型无关」部分——**本任务不强制 app 级 mock 模型**。

**最小可自动化：**

- `httptest` 首页含指向 `/login` 的链接；`/login` 返回 password form。  
- 不测完整 LLM：可测 skill 文本非空、prompt 短语、registry 描述更新即可。  
- 手工：`bin/pentgo` + 本地 fixture + 窄 intent「先发现登录入口并打印资产图，再 TASK_COMPLETE」。

- [ ] **Step 1:** 记录手工冒烟步骤到 plan 自查或 `docs` 一小节（gitignore 下可）。  
- [ ] **Step 2:** 若时间允许，加 `httptest` 单测只验证 skill Load 返回含 `ASSET MAP` / `login` 关键词。  
- [ ] **Step 3:** Commit if code; else skip.

### Task 4: 全量验证 + ARCHITECTURE 一句

**Files:**
- Modify: `docs/ARCHITECTURE.md`（Skills / 常见改哪：recon 入口发现）

```bash
go test ./... -count=1 -timeout 120s
go vet ./...
go build ./...
```

- [ ] **Step 1:** 全绿  
- [ ] **Step 2:** ARCHITECTURE 补一行 recon 职责  
- [ ] **Step 3:** Commit `docs: note recon entry-discovery guidance`

## 自查

- 无硬编码 phase；authz 仍是唯一 host 硬门。  
- recon skill 可操作、可与 credential 衔接。  
- prompt 可测短语存在。  
- 不默认外部站攻击；fixture/单测优先。

## 完成后

进入 **`2026-07-19-idor-dual-session-verification.md`**（计划 3）：在入口/登录证据具备后，扩展框架验证双身份 IDOR。
