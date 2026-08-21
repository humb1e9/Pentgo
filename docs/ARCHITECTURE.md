# PentGo 目录地图

PentGo 是一个中文交互的 Agent 终端工作台。目录只按稳定职责分类：业务状态、应用运行、外部适配和用户界面各有唯一位置，具体技术名只出现在 `adapters` 下。

## 文档入口

- 使用方式、配置与原生构建命令见[项目 README](../README.md)。
- 模块职责、运行时状态和持久化细节见[技术文档](TECHNICAL.md)。

## 目录

```text
cmd/
└── pentgo/                         进程入口、信号和依赖装配
internal/
├── app/                            用例、项目运行时、会话 worker 和事件
├── agent/                          厂商无关的消息、工具和模型协议
├── domain/                         Project、Session、Turn、Fact 纯状态
├── adapters/
│   ├── llm/                        Eino ADK 与模型 provider 适配
│   ├── mcp/                        MCP stdio/HTTP/SSE 与 Tool 适配
│   ├── storage/                    SQLite 持久化
│   └── skillfs/                    Markdown 技能文件加载
├── cli/                            REPL 命令和事件渲染
└── config/                         用户级 JSON 配置
skills/                             运行时 Markdown 技能数据
```

## 放置规则

| 代码回答的问题 | 目录 |
| --- | --- |
| 应用现在应执行哪个用例、谁拥有资源？ | `internal/app` |
| 模型、消息或工具需要遵守什么协议？ | `internal/agent` |
| 项目和会话状态允许怎样变化？ | `internal/domain` |
| 怎样连接某项外部技术或写入介质？ | `internal/adapters/<technology>` |
| 用户怎样通过终端操作应用？ | `internal/cli` |
| 进程怎样读取用户配置？ | `internal/config` |
| 进程怎样启动和装配依赖？ | `cmd/pentgo` |

不再使用 `contracts`、`execution`、`orchestrator`、`terminal` 这类相互重叠的顶层分类。新增外部实现进入 `adapters`；只有出现新的稳定职责时才增加顶层目录。

## 依赖方向

```text
cmd/pentgo ──→ config
    ├── cli ──→ app
    └─────────→ app
                  ├── agent
                  ├── domain
                  └── adapters
                       ├── llm ───────→ agent / config
                       ├── mcp ───────→ agent / storage / config
                       ├── storage ───→ agent / domain
                       └── skillfs
```

`domain` 不依赖 Eino、MCP、文件系统或终端。`agent` 只定义跨边界协议。`app` 负责运行时所有权和用例编排；`adapters` 负责协议、provider 与持久化细节；`cli` 只负责终端交互。

## 运行时所有权

```text
进程
└── 一个 app.ProjectRuntime
    └── <cwd>/.pentgo/
        ├── pentgo.db
        ├── 共享 Local Backend、多个 MCP client 和工具 provider
        └── 多个 app.SessionWorker
            ├── SQLite transcript/session rows
            └── turn 事件
```

- `ProjectRuntime` 拥有项目 context、evidence、blackboard、Local Backend、MCP provider、transcript handle 和 worker。每个新建的 Eino Agent 都用该 Local Backend 注册 `ls`、`read_file`、`write_file`、`edit_file`、`glob`、`grep` 和 `execute`。每个具名 MCP Server 维护独立的 stdio、Streamable HTTP 或 SSE 连接；工具以其原始全局唯一名称暴露。
- `SessionWorker` 自己的 goroutine 才能修改 `domain.Session`；`Snapshot` 返回原子发布的深拷贝。
- 一个 session 同时只运行一个 turn；同一项目内不同 session 可以并发运行。
- `pentgo` 打开 `<cwd>/.pentgo/` 并创建新会话；目录不存在时同时创建工作区。`pentgo resume` 在进入 TUI 前列出会话并恢复用户选择的历史会话。
- TUI 只承载当前会话，不提供历史会话切换；新建、重命名、删除等项目操作都在 TUI 中完成。
- `Ctrl+C` 或 `Ctrl+D` 退出 TUI 时停止 worker；会话历史和最后快照会持久化，后续启动可以继续使用。
- worker 事件通过有界 channel 发布，CLI 可以消费而不阻塞模型执行。

## Turn 顺序

```text
app.SessionWorker
  → app.TurnService.BeginTurn
  → transcript.Append(user)
  → agent.ModelEngine.Run(transcript replay + tools)
  → transcript.Append(assistant/tool messages in order)
  → evidenceTool persists tool result before returning it to the model
  → app.TurnService.FinishTurn
  → app.ProjectRuntime.PersistState
```

正常完成的 turn 不会关闭 session。模型适配器不保存 session、project、blackboard 或 checkpoint；重启后的正常继续始终从完整 transcript 创建新模型运行，历史 tool message 只回放，不再次调用工具。

## 持久化边界

```text
<cwd>/.pentgo/
├── pentgo.db                    项目唯一持久化事实源
└── tmp/                         MCP 工作目录和临时文件
```

`pentgo.db` 使用 SQLite WAL、foreign keys 和版本化 schema。核心数据按关系建模：`projects`、`sessions`、`session_targets`、`turns`、`facts`、`evidence_records`、`transcript_messages` 和 `transcript_tool_calls`。provider 参数保留为 JSON 列，领域事实不存 JSON blob。

project 的 session summaries 从 `sessions` 查询派生。`CommitSession` 原子保存 session、turn、targets 和 project 元数据；`SaveBlackboard` 原子保存共享事实。内存快照只在事务提交后发布。


## Tool 边界

`agent.Tool` 只有名称、描述和 `Invoke(context.Context, map[string]any)`。需要保留 provider schema 的工具额外实现 `agent.ToolSchemaProvider`。`adapters/llm` 将其转换为 ADK tool；`adapters/mcp` 将 MCP schema 转为同一协议。

应用层提供 `write_project_fact` 和按需启用的 `load_skill`。Eino Local Backend 工具随 Agent 初始化注册，路径锚定在工作目录；外部 MCP 工具先执行，再由应用层的 evidence decorator 写入证据。两类执行工具都返回带有 `[evidence_ref: N]` 的持久化结果。

## Skills

`skillfs.Registry` 构造时接收显式 `fs.FS`，不读取当前工作目录，也不使用包级默认 registry。`/load_skill` 枚举顶层 `*.md`，只把名称和描述摘要放进提示词；模型随后用准确名称加载单个正文。

## 验证

```bash
go test ./... -count=1
go test -race ./...
go vet ./...
go build ./cmd/...
git diff --check
```
