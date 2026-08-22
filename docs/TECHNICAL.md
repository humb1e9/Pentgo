# PentGo 技术文档

本文档说明 PentGo 的实现边界、运行时模型与开发验证方式。安装、配置和日常使用请参阅[项目 README](../README.md)。

## 1. 设计目标

PentGo 是一个以终端为入口的中文 Agent 运行时。它将模型调用、工具调用、会话状态、项目级共享记录和本地持久化组合为可持续运行的工作流。

实现重点包括：

- 让模型供应商、工具供应商与领域状态保持解耦。
- 让每个会话的可变状态只有一个并发写入方。
- 在工具结果返回模型前保留可查询的执行记录。
- 在进程重启后从对话记录恢复上下文，而不重复执行历史工具。
- 允许本地工作区工具、MCP 工具和 Markdown skills 按统一方式加入每轮运行。

## 2. 目录与依赖方向

```text
cmd/
└── pentgo/                         进程入口、信号和依赖装配
internal/
├── app/                            用例、项目运行时、会话 worker 和事件
├── agent/                          厂商无关的消息、工具和模型协议
├── domain/                         Project、Session、Turn、Fact 纯状态
├── adapters/
│   ├── builtins/                   工作区文件与命令后端
│   ├── llm/                        Eino ADK 与模型 provider 适配
│   ├── mcp/                        MCP stdio、HTTP、SSE 与 Tool 适配
│   ├── skillfs/                    Markdown 技能文件加载
│   └── storage/                    SQLite 持久化
├── cli/                            Bubble Tea 终端界面和命令处理
└── config/                         用户级 JSON 配置
skills/                             运行时 Markdown 技能数据
```

依赖方向保持单向：

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

`domain` 不依赖 Eino、MCP、文件系统或终端。`agent` 只定义跨边界协议。`app` 拥有运行时资源并编排用例；`adapters` 处理外部协议、模型、存储和文件系统细节；`cli` 只处理终端交互。

## 3. 运行时模型

一个进程只创建一个 `app.ProjectRuntime`。它加载项目元数据、共享记录、工作区后端、执行记录存储、MCP 连接和多个会话 worker。

```text
进程
└── app.ProjectRuntime
    └── <cwd>/.pentgo/
        ├── pentgo.db
        ├── 共享 Local Backend、多个 MCP client 和工具 provider
        └── 多个 app.SessionWorker
            ├── SQLite transcript/session rows
            └── turn 事件
```

`SessionWorker` 在独立 goroutine 中串行处理一个会话的请求。该 goroutine 是 `domain.Session` 的唯一写入者；外部调用方只能提交请求、读取原子发布的深拷贝快照和消费有界事件流。

每个会话一次只运行一个 turn，不同会话可以并行。事件 channel 满时会丢弃最旧的临时事件，不会阻塞模型或工具执行；持久化 transcript 始终是可恢复的事实来源。

## 4. 一轮任务的执行顺序

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

`TurnService` 在发布界面事件前先持久化每条 transcript 消息。失败、取消和中断会被映射为相应的 turn 状态，并在返回调用方前保存最后快照。

模型适配器是短生命周期对象，不保存 session、project、共享记录或 checkpoint。下一轮或进程恢复时，系统从完整 transcript 创建新的模型运行。

## 5. 工具接入模型

工具协议位于 `internal/agent/tool.go`：

```go
type Tool interface {
    Name() string
    Description() string
    Invoke(context.Context, map[string]any) (string, error)
}
```

可选的 `ToolSchemaProvider` 提供 JSON Schema，使适配器保留参数类型和必填字段。`ToolProvider` 在每轮运行时解析可用工具，`ToolCloser` 用于释放底层连接。

工具集合由以下部分组成：

- Eino Local Backend 提供 `ls`、`read_file`、`write_file`、`edit_file`、`glob`、`grep` 和 `execute`。
- 应用层提供 `write_project_fact`，用于写入项目级共享记录。
- 应用层在启动时发现至少一个有效技能后，向模型提供 `load_skill`。
- MCP 服务发现到的工具通过同一协议加入本轮集合。

工作区后端拒绝绝对路径、目录跳转和解析后离开根目录的符号链接路径。命令执行会显式切换到工作区目录后再运行。

## 6. MCP 连接与工具聚合

`internal/adapters/mcp` 支持三种传输：

- `stdio`，通过 `command` 和 `args` 启动子进程。
- Streamable HTTP，使用 `type: "http"` 和 `url`。
- SSE，使用 `type: "sse"` 和 `url`。

项目运行时在打开项目时连接全部具名服务、读取其工具目录，并将工具聚合到统一 provider。服务名和工具名需满足 `[A-Za-z0-9_-]`；不同服务提供的工具名必须全局唯一。

MCP 工具保留服务声明的描述与 JSON Schema。调用完成后，应用层装饰器会记录参数、状态和输出，再将带记录编号的输出返回给模型。配置中的环境变量值和 HTTP Header 值会在输出中被替换为脱敏标记。

## 7. 技能目录与按需加载

`skillfs.Registry` 接收显式 `fs.FS`，不读取包级全局状态。PentGo 在每次进程启动时扫描一次顶层 `*.md`，要求每份有效技能提供非空 YAML frontmatter `description`；描述经过本地标准化和长度限制后形成稳定 digest。无法读取、frontmatter 格式错误或缺少描述的单份技能会被跳过，并作为终端本地诊断显示，不会阻断其它技能。

每个新建或恢复的会话都会收到一条由宿主写入 transcript 的 digest 版本化目录 system message；未变化的目录不会重复注入，修改 `skills/` 后重启 PentGo 才会重新扫描，并在恢复会话时以新目录显式替换旧目录。后续 turn 只回放该上下文，不重新扫描。完整正文在模型以目录中的准确名称调用 `load_skill` 后才读取。单份正文限制为 32 KiB，避免单个文件无限占用模型上下文。

原生运行默认使用：

```text
<working-directory>/skills
```

## 8. SQLite 数据模型与恢复机制

项目状态位于：

```text
<working-directory>/.pentgo/
├── pentgo.db
└── tmp/
```

数据库使用 SQLite WAL、外键、5 秒 busy timeout 和版本化 schema。核心关系表包括：

```text
projects
sessions
session_targets
turns
facts
evidence_records
transcript_messages
transcript_tool_calls
```

`CommitSession` 在同一事务中保存 session、当前 turn、目标和项目元数据；`SaveBlackboard` 原子替换共享事实。内存状态仅在事务提交后发布。

`TranscriptStore` 按 `(session_id, seq)` 保存消息，并在助手消息的子表中保存工具调用顺序和原始参数。恢复时，历史消息按原顺序重放给模型；历史 tool message 只用于恢复上下文，不会再次触发工具调用。

`EvidenceStore` 为每次工具结果分配递增记录编号，并将编号附加到返回文本。记录写入失败后存储会进入失败状态，避免后续输出被误认为已完整保存。

## 9. 配置模型

`internal/config` 从用户级 `config.json` 加载 `agent` 配置，并为缺失字段补齐默认值。支持的模型 provider 为：

- `openai`，可用于 OpenAI 与兼容接口。
- `anthropic`。

模型配置包含 `base_url`、`model`、`api_key` 或 `api_key_env`。API Key 优先读取显式字段，否则读取指定环境变量；凭据不写入项目数据库。

`agent.mcp` 是具名 MCP 服务映射。旧版单服务对象会被读取为 `default` 服务，以保持配置兼容性。

## 10. 原生构建与开发验证

```bash
go test ./... -count=1
go test -race ./...
go vet ./...
go build ./cmd/...
git diff --check
```

用户环境需先安装 Go `1.25` 或更新版本。完整的原生构建和启动命令见[项目 README](../README.md)。
