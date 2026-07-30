# PentGo 目录地图

> 读代码前先看本文。目标：5 分钟内知道「改功能该进哪个目录」。  
> `docs/` 下除本文件外默认 gitignore；本文件会进仓库。

## 一眼结构

```
PentGo/
├── cmd/pentgo/              # 唯一可执行入口（REPL main）
├── internal/                # 业务源码（不对外 import）
│   ├── agent/               # Eino 模型构造（OpenAI / Anthropic）
│   ├── app/                 # 组装一次 engagement
│   ├── config/              # 用户配置加载
│   ├── report/              # 报告生成 + engagement 落盘
│   ├── terminal/            # REPL 交互
│   └── runtime/             # ★ 运行时（已拆为 5 个子包）
│       ├── evidence/        # engagement-local JSONL journal
│       ├── mcp/             # 单一 local stdio MCP bridge
│       ├── exec/            # 本地预检 / 执行
│       ├── authz/           # Scope + Authorizer（依赖 exec.CodeBlock）
│       ├── session/         # Target + AgentSession
│       └── loop/            # Eino Runner / Tools / Prompt
├── skills/                  # 可加载 Skill（SKILL.md + registry.go）
├── docs/                    # ARCHITECTURE.md（跟踪）+ superpowers/（本地计划）
├── bingo/                   # 参考 Python 树（gitignore）
├── bin/                     # 本地构建产物（gitignore）
├── README.md
└── AGENTS.md
```

## runtime 子包依赖（禁止环）

```
exec  →  authz
  ↑        ↑
  └── loop ┴── evidence / session
```

| 包 | 职责 | 关键类型 |
|----|------|----------|
| `exec` | 预检与本地进程执行 | `CodeBlock`, `Executor` |
| `authz` | 主机范围 + 破坏性操作门 | `Scope`, `Authorizer` |
| `evidence` | engagement-local JSONL journal 与引用查询 | `Journal`, `Record` |
| `mcp` | 单一 stdio MCP 发现与 Eino 工具转换 | `ConnectStdio`, `Client` |
| `session` | 目标解析与会话状态 | `Target`, `AgentSession` |
| `loop` | 单一 Eino Agent、工具与自然终止 | `Runner`, `RunEino` |

## 运行时数据流

```
用户 REPL 输入（含 URL）
  → terminal.ParseTask → sess.Target + Intent
  → app.Service.Run
       eng writer / evidence journal / optional local stdio MCP discovery / Eino model
       exec.NewExecutor
       loop.NewRunner（Authorizer, Scope hosts, Journal, discovered MCP tools）
       runner.RunEino → 自然终止
       MCP.Close + Journal.Close + Runtime 脚本清理
       report.Publish → session.json + report.md
```

## 测试布局（已跟包走）

```
internal/runtime/evidence/journal.go
internal/runtime/evidence/journal_test.go   ← 同目录，package evidence
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
| 模型循环 / 工具 | `internal/runtime/loop` |
| 证据 journal / 引用 | `internal/runtime/evidence` |
| MCP stdio bridge | `internal/runtime/mcp` |
| 执行 / 预检 | `internal/runtime/exec` |
| 授权范围 | `internal/runtime/authz` |
| session 字段 | `internal/runtime/session` |
| report 渲染 | `internal/report/markdown.go` |
| 加 skill | `skills/<name>/` + `registry.go` |

## 刻意没做的

- skills 仍扁平（分子目录会动全部 embed）
- bingo 仍只读参考、不参与 build
