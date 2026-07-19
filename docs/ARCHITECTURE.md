# PentGo 目录地图

> 读代码前先看本文。目标：5 分钟内知道「改功能该进哪个目录」。  
> `docs/` 下除本文件外默认 gitignore；本文件会进仓库。

## 一眼结构

```
PentGo/
├── cmd/pentgo/              # 唯一可执行入口（REPL main）
├── internal/                # 业务源码（不对外 import）
│   ├── agent/               # 模型客户端（OpenAI 兼容 / Anthropic）
│   ├── app/                 # 组装一次 engagement
│   ├── config/              # 用户配置加载
│   ├── report/              # 报告生成 + engagement 落盘
│   ├── terminal/            # REPL 交互
│   └── runtime/             # ★ 运行时（已拆为 5 个子包）
│       ├── exec/            # 代码块解析 / 预检 / 执行 / 证据等级
│       ├── authz/           # Scope + Authorizer（依赖 exec.CodeBlock）
│       ├── verify/          # FINDING 解析 + HTTP 验证 + 确定性 Score
│       ├── session/         # Target + AgentSession（依赖 verify.VerificationResult）
│       └── loop/            # Runner 模型循环 / History / Prompt / 报告上下文
├── skills/                  # 可加载 Skill（SKILL.md + registry.go）
├── docs/                    # ARCHITECTURE.md（跟踪）+ superpowers/（本地计划）
├── bingo/                   # 参考 Python 树（gitignore）
├── bin/                     # 本地构建产物（gitignore）
├── README.md
└── AGENTS.md
```

## runtime 子包依赖（禁止环）

```
exec  →  authz  →  verify  →  session  →  loop
  ↑         ↑          ↑           ↑          │
  └─────────┴──────────┴───────────┴──────────┘
            （loop 可依赖全部叶子包）
```

| 包 | 职责 | 关键类型 |
|----|------|----------|
| `exec` | 块提取、预检、进程执行、EvidenceSink | `CodeBlock`, `Executor`, `EvidenceLevel` |
| `authz` | 主机范围 + 破坏性操作门 | `Scope`, `Authorizer` |
| `verify` | 框架自发 HTTP、登录 jar、Score | `HTTPVerifier`, `FindingSpec`, `VerificationResult` |
| `session` | 目标解析与会话状态 | `Target`, `AgentSession` |
| `loop` | 模型循环、consolidation、报告上下文 | `Runner`, `History`, `ReportContext` |

## 运行时数据流

```
用户 REPL 输入（含 URL）
  → terminal.ParseTask → sess.Target + Intent
  → app.Service.Run
       eng writer / agent client
       exec.NewExecutor
       loop.NewRunner（Authorizer, Scope hosts, verify.HTTPVerifier, EvidenceSink）
       runner.Run → SessionDone?
       runner.ConsolidateAndVerify  # 解析 FINDING → 框架验证
       loop.ValidateReportContext
       report.GenerateTerminalMarkdown + PublishWithReport
```

## 测试布局（已跟包走）

```
internal/runtime/verify/http_verifier.go
internal/runtime/verify/http_verifier_test.go   ← 同目录，package verify
```

- **不再使用** `tests/_packages` 与符号链接
- 白盒测试与源码同 package、同目录（Go 标准）
- 跑：`go test ./... -count=1`

## Skills

- `skills/<name>/SKILL.md`
- 注册表 `skills/registry.go`：`//go:embed` + `descriptions` map 为唯一真源
- **`recon`**：同 scope 入口/资产发现、登录与 API 信号、`PENTGO ASSET MAP` 输出；为 `credential`/已认证验证提供证据（非硬编码 phase）

## 产物

| 路径 | 含义 |
|------|------|
| `./eng-<id>/` | 默认 cwd 发布目录 |
| `output/` | 可选输出根（gitignore） |
| `bin/pentgo` | 构建产物 |

## 常见「我该改哪」

| 你想… | 去… |
|--------|-----|
| REPL / 任务解析 | `internal/terminal` |
| engagement 接线 | `internal/app/engagement.go` |
| 模型循环 / consolidation | `internal/runtime/loop` |
| 漏洞评分 / 类型 | `internal/runtime/verify` |
| 框架 HTTP / 登录 | `internal/runtime/verify/http_verifier.go` |
| 执行 / 预检 | `internal/runtime/exec` |
| 授权范围 | `internal/runtime/authz` |
| session 字段 | `internal/runtime/session` |
| report 发现段 | `internal/report/findings.go` |
| 加 skill | `skills/<name>/` + `registry.go` |

## 刻意没做的

- skills 仍扁平（分子目录会动全部 embed）
- bingo 仍只读参考、不参与 build
