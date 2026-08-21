# IDOR / 双会话验证 实施计划（计划 3，在 recon 之后）

> **For agentic workers:** 先完成 `2026-07-19-recon-entry-discovery.md`，再执行本计划。  
> REQUIRED SUB-SKILL: superpowers:subagent-driven-development 或 executing-plans。

**Goal:** 补齐 SRC 高频「对象级越权」的框架验证：在**两个已认证身份**（或「已认证 vs 匿名」已有能力之外）下，对同一资源 URL 做差分，由框架持有双 cookie jar、自发请求、确定性评分，失败恒降级。

## 问题

- 现有 `LoginURL` 只建**一个**会话；payload 带 cookie、baseline 匿名——覆盖「需登录才能看」，**不覆盖**「用户 A 可读用户 B 的 `/user/2`」。  
- `VulnAuthBypass` 签名偏 admin 关键字 / 302→200，对「同角色跨 ID」IDOR 不足。  
- bingo 风格：差分证据必须来自框架字节，不能靠模型 stdout。

## 非目标

- 不硬编码业务路径；`user_a`/`user_b` 登录参数由 FINDING 块声明。  
- 不替代 recon；登录 URL/账号必须来自执行证据（与 credential 纪律一致）。

## 设计

### FindingSpec 扩展

```
login_url / login_body / login_method / ...   # 身份 A（已有）
login_url_b / login_body_b / login_method_b / username_b  # 身份 B（新）
url:          # 以 A 会话访问的资源（应为 B 的对象）
baseline_url: # 可选：以 B 会话访问同一资源（应 200 且为本人）或匿名
```

语义（推荐默认）：

| 模式 | 条件 | payload | baseline | 差分含义 |
|------|------|---------|----------|----------|
| auth_vs_anon | 仅 A 登录 | A+url | 匿名 baseline_url | 已有 |
| idor_a_reads_b | A+B 登录 | A+url | B+url（同 URL） | A 拿到 B 才该有的数据 |
| horizontal | A+B，url 含 B 的 id | A 访问 B 资源 | A 访问 A 资源 | 可选后续 |

本计划实现 **idor_a_reads_b** + 保留 auth_vs_anon。

### 新类型或复用

- 新增 `VulnType = "idor"`（推荐，报告清晰），或复用 `auth_bypass` 并加 P5 分支。  
- **决策：新增 `idor`**，`knownVulnType` / consolidation 提示同步。

### HTTPVerifier

1. `verifyLogin` A → jarA / cookie header A  
2. 若 `LoginURLB != ""`：`verifyLogin` B → cookie header B  
3. payload 请求：Cookie=A  
4. baseline 请求：若 B 已验证则 Cookie=B 且 URL=baseline 或同 url；否则匿名  
5. `Score`：`VulnIDOR` 确定性条件示例：  
   - payload body ≠ baseline body  
   - payload 含敏感信号或长度/关键字段与 baseline 不同  
   - 可选：payload 含 `username_b` / 对象 id  
   - 登录失败 → checksFailed，不升级  

### 落盘脱敏

- `login_cookie_names` / `login_b_cookie_names`  
- 不落完整 cookie 值与明文 password（curl REDACTED，沿用现逻辑）

### 报告

- `RenderVerifiedFindings` 对 `idor` 显示 dual login verified 标志。

## 任务概要

1. **T1** `FindingSpec` + parse `login_*_b` + `VulnIDOR` — **done** (`8783219`)
2. **T2** `VerifyWithEvidence` 双登录编排 + 记录字段 — **done**
3. **T3** `Score`/`deterministicCheck` idor 分支（权重不改阈值表，只加 P5 条件） — **done** (`ResponseDiffers` 忠实 bingo)
4. **T4** evidence + report 渲染 — **done**
5. **T5** consolidation 提示词 + httptest 双用户 fixture 测试 — **done**
6. **T6** 全量 `go test ./...` — **done**

## 全局约束

- 仅标准库；测试跟包走。  
- 非幂等：每个登录一次；GET 资源可 reproductions 次。  
- scope 校验 A/B 登录与资源 URL。  
- 恒降级。

## 与计划 2 的依赖

- recon 负责让模型**找到**双账号/对象 URL 证据。  
- 本计划负责框架**复现** IDOR 判定。无 recon 时仍可用 fixture 单测 IDOR 引擎。

## 自查

- [x] A/B 登录失败不升级（`auth session B not established`；`TestVerifyWithEvidenceIDORNoDiffRefutes`）
- [x] 同响应无差分 → 非 VERIFIED（`TestScoreIDORNoDiffIsNotVerified` / dual fixture）
- [x] 脱敏（cookie 值不落盘；沿用 credential curl REDACTED）
- [x] 无 phase 硬编码（FINDING 声明双会话，无站点路径猜解）  
