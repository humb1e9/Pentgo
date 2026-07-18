# PentGo 目录地图

> 读代码前先看本文。目标：5 分钟内知道「改功能该进哪个目录」。  
> `docs/` 默认被 `.gitignore` 忽略（本地规划用）；若你从仓库克隆看不到本文，问维护者要这份文件。

## 一眼结构

```
PentGo/
├── cmd/pentgo/          # 唯一可执行入口（REPL main）
├── internal/            # 业务源码（不对外 import）
│   ├── agent/           # 模型客户端（OpenAI 兼容 / Anthropic）
│   ├── app/             # 组装一次 engagement：配置 → runner → 报告 → 发布
│   ├── config/          # 用户配置加载（~/.config/pentgo/config.json）
│   ├── runtime/         # ★ 核心：执行循环、授权、验证、证据等级
│   ├── report/          # 报告生成 + engagement 落盘（session/report/evidence）
│   └── terminal/        # REPL 交互、任务解析、串行 engagement
├── skills/              # 可加载 Skill（每目录一个 SKILL.md + embed 注册表）
├── tests/_packages/     # 测试真身（生产目录里的 *_test.go 是相对符号链接）
├── bin/                 # 本地构建产物（gitignore）
├── bingo/               # 参考的 Python 原版（gitignore，只读对照）
├── docs/                # 计划/规格/本架构图（gitignore）
├── README.md            # 用户向：怎么跑、怎么配
└── AGENTS.md            # Agent 约束（CTF/本地 fixture 纪律）
```

## 运行时数据流（读代码顺序）

```
用户在 REPL 输入含 URL 的自然语言
        │
        ▼
terminal.ParseTask  →  Target + Intent
        │
        ▼
app.Service.Run
  ├─ report.NewEngagementWriter   # staging: evidence/ + work/
  ├─ agent.Client                 # 模型
  ├─ runtime.NewExecutor          # 跑 Python/Bash 块
  ├─ runtime.NewRunner            # 模型循环
  │     Authorizer + Scope        # 执行前硬门
  │     HTTPVerifier              # 框架自发 HTTP 验证
  │     EvidenceSink=writer       # 执行/验证证据落盘
  ├─ runner.Run                   # 直到 SessionDone / Fail / Cancel
  ├─ runner.ConsolidateAndVerify  # 仅 SessionDone：解析 FINDING → 框架验证
  ├─ ValidateReportContext        # 声明 vs 执行等级交叉检查
  ├─ report.GenerateTerminalMarkdown  # 模型写叙述
  └─ writer.PublishWithReport     # 原子发布 session.json + report.md + evidence/
```

**关键纪律**

| 层 | 职责 | 位置 |
|----|------|------|
| 决策层 | 系统提示防拒答、Skill 指导 | `runtime/prompt.go`、`skills/*` |
| 执行硬门 | 破坏性/越权主机拦截 | `runtime/authorization.go`、`scope.go` |
| 反幻觉 | 框架自持 HTTP + 确定性 Score | `runtime/http_verifier.go`、`verification.go` |
| 报告事实 | findings 数据驱动渲染，模型只写叙述 | `report/findings.go`、`artifacts.go` |

## `internal/runtime` 文件怎么找

包大但职责稳定，按主题打开：

| 主题 | 文件 |
|------|------|
| 会话状态 | `session.go` |
| 模型循环 | `runner.go`、`history.go` |
| 代码块解析/预检/执行 | `blocks.go`、`preflight.go`、`executor.go` |
| 授权与范围 | `authorization.go`、`scope.go`、`target.go` |
| 发现声明解析 | `finding_spec.go`、`finding_label.go` |
| 框架验证 | `http_verifier.go`、`verification.go` |
| 报告上下文与审计 | `report_context.go`、`validation.go`、`evidence_grade.go` |
| 系统提示 / 拒答恢复 | `prompt.go`、`refusal.go` |

## Skills

- 每个 skill：`skills/<name>/SKILL.md`
- 注册表：`skills/registry.go`  
  - `//go:embed .../SKILL.md` 多行累加进 `skillFS`  
  - `descriptions` map = **已登记 skill 的唯一真源**（加 skill 必须同时加 embed + map 项）
- 模型通过 `SKILL_LOAD: name` 加载；内容进 history，不自动执行攻击

当前多为 Web 漏洞方法论 skill + `recon` / `terminal` / `waf-bypass` 等通用项。  
**暂未按子目录分组**（若以后分组，必须同步改 embed 路径）。

## 测试布局（容易懵）

```
internal/runtime/foo_test.go  →  符号链接
tests/_packages/internal/runtime/foo_test.go  ← 真身
```

- 改断言、加测试：**改 `tests/_packages/...` 真身**
- 新增测试文件：在 `tests/_packages/...` 写文件，再到源码目录建**相对**符号链接  
  例：`ln -s ../../tests/_packages/internal/runtime/new_test.go internal/runtime/new_test.go`
- 测试 package 名 = 被测包名（不是 `xxx_test` 外部测试包）

跑：

```bash
go test ./internal/runtime ./internal/report ./internal/app -count=1
go test ./... -timeout 120s
```

## 产物落在哪

| 路径 | 含义 |
|------|------|
| 运行时默认 `./eng-<id>/` | 一次 engagement 发布目录（`session.json`、`report.md`、`evidence/`、`work/`） |
| `/tmp/pentgo-*/` | 历史冒烟/调试（**不是**仓库结构的一部分） |
| `bin/pentgo` | `go build -o bin/pentgo ./cmd/pentgo` |

根目录不应长期堆 `eng-*`；本地可建 `output/` 再跑（见 `.gitignore` 的 `/eng-*/`、`/output/`）。

## docs/ 怎么读

```
docs/
├── ARCHITECTURE.md          # 本文
└── superpowers/
    ├── plans/               # 可执行实施计划（按日期）
    └── specs/               # 设计规格（偏早期）
```

- 现行能力相关计划看日期较新的 `2026-07-17` / `2026-07-18`（验证、报告、认证会话等）
- 早期 `2026-07-16` 的 recon/scan/phase 多是演进前方案，**不要当当前架构**

## bingo/ 是什么

Python 红队框架参考树，**gitignore**，不参与 `go build`。对照反幻觉/登录验证时可只读；不要把 bingo 的 phase 硬编码搬进 PentGo。

## 配置

- 路径：`${XDG_CONFIG_HOME:-~/.config}/pentgo/config.json`（见 README）
- 加载：`internal/config`
- 授权门字段：`agent.authorization`（`allowed_hosts`、`allow_destructive`、`allow_private_hosts`）

## 常见「我该改哪」

| 你想… | 去… |
|--------|-----|
| 改 REPL 提示/命令 | `internal/terminal` |
| 改 engagement 接线顺序 | `internal/app/engagement.go` |
| 改模型循环/consolidation | `internal/runtime/runner.go` |
| 改漏洞评分/类型 | `internal/runtime/verification.go` |
| 改框架 HTTP 验证/登录 | `internal/runtime/http_verifier.go` |
| 改 FINDING 块字段 | `internal/runtime/finding_spec.go` |
| 改 report.md 发现段 | `internal/report/findings.go`、`artifacts.go` |
| 加/改 skill 文案 | `skills/<name>/SKILL.md` + `registry.go` |
| 加测试 | `tests/_packages/...` + 符号链接 |

## 刻意没做的整理（以后可选）

- 不拆 `internal/runtime` 为多包（避免 import 大爆炸）
- 不把 skills 分子目录（会动全部 `//go:embed`）
- 不把测试真身移回包内（当前符号链接约定已全仓使用）
