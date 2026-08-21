# PentGo

PentGo 是一个基于 Go、Eino 和 Eino ADK 的中文渗透测试智能体。它提供持续对话、多会话、项目共享事实、证据日志、MCP 工具和可回放的会话历史。

## 快速开始

### 环境

- Go `1.25` 或更高版本
- Linux、WSL 或 macOS
- 支持 Eino tool calling 的 OpenAI 兼容模型或 Anthropic 模型
- 可选的 stdio、Streamable HTTP 或 SSE MCP Server

### 构建

```bash
go build -o pentgo ./cmd/pentgo
```

### 配置模型

配置文件位置：

| 系统 | 路径 |
| --- | --- |
| Linux / WSL | `${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json` |
| macOS | `$HOME/Library/Application Support/pentgo/config.json` |

推荐使用环境变量保存 API key：

```bash
export OPENAI_API_KEY="TOKEN"
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/pentgo"
```

最小配置：

```json
{
  "agent": {
    "provider": "openai",
    "max_turns": 20,
    "request_timeout_seconds": 60,
    "max_output_bytes": 65536,
    "openai": {
      "base_url": "https://api.openai.com/v1",
      "model": "MODEL",
      "api_key_env": "OPENAI_API_KEY"
    }
  }
}
```

Anthropic 使用 `provider: "anthropic"` 和 `agent.anthropic` 配置。OpenAI 兼容服务只需把 `base_url` 和 `model` 换成对应值。

### 启动

```bash
go run ./cmd/pentgo
```

启动目录就是工作区边界。首次启动会在当前目录创建 `.pentgo/` 和一个交互会话；之后每次执行 `pentgo` 都会创建一条新会话。项目数据不会写入父目录或自动选择兄弟目录。

### 进程命令

```bash
pentgo
pentgo resume
```

- `pentgo` 打开当前目录工作区并创建新会话；首次使用时同时创建工作区。
- `pentgo resume` 仅打开已有工作区，在进入 TUI 前列出现有会话供选择，不会创建工作区或会话。

## 交互命令

启动后进入 Bubble Tea 全屏终端界面：全宽显示当前会话历史、工具活动和输入框。`/new` 创建新的空白会话；历史会话仅通过 `pentgo resume` 在进入 TUI 前选择。

```text
/load_skill
/new
/session rename NAME
/session list
/session delete [SESSION_ID]
/status
/blackboard
/clear
/exit
```

`/load_skill` 使用显式注入的技能文件系统扫描顶层 `skills/*.md`，只加载名称和描述摘要；模型需要正文时调用 `load_skill`。没有执行 `/load_skill` 仍可进行普通对话。

每个 Agent 初始化时都注册 Eino Local Backend 的内置工具：`ls`、`read_file`、`write_file`、`edit_file`、`glob`、`grep` 和 `execute`。它们以启动目录为根，文件路径使用相对路径；所有调用结果都会写入项目 evidence。MCP 工具是额外能力，不是这些本地工具的前置条件。

## 架构

```text
cmd/pentgo → cli → app
                    ├── agent / domain
                    └── adapters/{llm,mcp,storage,skillfs}
```

- `internal/app`：用例编排、ProjectRuntime、SessionWorker、事件和生命周期。
- `internal/agent`：与模型厂商无关的消息、工具和 ModelEngine 协议。
- `internal/domain`：Project、Session、Turn、Fact 和状态转换。
- `internal/adapters/llm`：Eino ADK loop、消息转换和 provider 构造。
- `internal/adapters/builtins`：内置工作区文件系统和命令执行工具。
- `internal/adapters/mcp`：MCP stdio、Streamable HTTP、SSE 发现、schema 和工具调用。
- `internal/adapters/storage`：项目、session、transcript、evidence 持久化。
- `internal/adapters/skillfs`：接收显式 `fs.FS` 的技能文件加载器。
- `internal/cli`：REPL 命令解析和事件渲染。
- `internal/config`：用户级运行配置。

一个进程只有一个 ProjectRuntime，一个项目可以同时拥有多个 SessionWorker。worker goroutine 是 session 可变状态的唯一写入者，CLI 只消费快照和有界事件流。

## 持久化与恢复

项目数据位于工作目录的 `.pentgo/`：

```text
<working-directory>/
└── .pentgo/
    ├── pentgo.db
    └── tmp/
```

- `pentgo.db` 是项目唯一的持久化事实源，使用 SQLite WAL、外键和事务。
- project、session、turn、target、fact、evidence 和 transcript 使用规范化关系表；只有 provider 参数这类开放结构使用 JSON 列。
- project 的 session summaries 从会话表派生，不维护重复索引文件。
- session 与 project 元数据同事务提交；blackboard 事实在独立事务中原子替换。
- evidence 使用数据库分配的递增 `evidence_ref`；提交成功后工具结果才返回模型。
- transcript 按 `(session_id, seq)` 排序，tool calls 存在子表中，历史消息只回放，不会再次调用工具。
- 普通继续路径是 transcript replay。历史 tool message 只回放，不会再次调用工具；模型适配器不保存领域状态。

正常完成一轮 turn 后，下一条用户消息会进入同一个 session。按 `Ctrl+C` 或 `Ctrl+D` 退出 TUI 时，活动 turn 会保留已写入的 evidence 并标记为 `interrupted`；使用 `pentgo resume` 选择该会话后从 transcript 继续。

## MCP

配置多个 MCP Server：

```json
{
  "agent": {
    "mcp": {
      "nmap": {
        "command": "/absolute/path/to/nmap-mcp-server",
        "args": ["--stdio"],
        "env": {"NMAP_PATH": "/usr/bin/nmap"}
      },
      "browser": {
        "command": "/absolute/path/to/browser-mcp-server",
        "args": ["--stdio"]
      },
      "assets": {
        "type": "http",
        "url": "http://127.0.0.1:80/mcp",
        "headers": {
          "Authorization": "Bearer TOKEN"
        }
      },
      "legacy": {
        "type": "sse",
        "url": "http://127.0.0.1:8081/sse"
      }
    }
  }
}
```

项目 runtime 拥有全部 MCP client 的连接和关闭生命周期。每个配置项使用 `command`（默认 `stdio`）、`type: "http"`（Streamable HTTP）或 `type: "sse"`（旧版 SSE）建立独立连接；模型直接看到 Server 声明的原始工具名，例如 `nmap_scan` 和 `asset_query`，PentGo 仅在内部按 Server 路由调用。所有外部工具名必须在当前配置中全局唯一。`headers` 会附加到 HTTP/SSE 请求。旧的单 Server 对象会读为 `default`。Server 和工具名称必须匹配 `[A-Za-z0-9_-]`；每个外部工具调用经过 evidence decorator，调用参数和结果写入当前项目 journal。

## 验证

```bash
go test ./... -count=1
go test -race ./...
go vet ./...
git diff --check
```
