# PentGo

> 基于 Go、Eino ADK 和 MCP 的中文 AI Agent 终端工作台。

PentGo 在终端中提供连续对话、工具调用、会话管理、共享上下文和本地持久化能力。它将工作状态保存在当前目录，关闭进程后仍可继续处理历史任务。

## 功能

- 通过终端对话驱动模型分析任务、调用工具并持续处理结果。
- 支持新建、重命名、删除和恢复多个独立会话。
- 支持本地文件、文本检索、文件编辑和命令执行等工作区工具。
- 支持通过 MCP 扩展远程工具，并聚合多个 stdio、HTTP 或 SSE 服务。
- 持久化对话、工具结果和共享事实，进程退出后可恢复历史工作状态。
- 支持从技能目录发现摘要，并按需加载单份 Markdown 技能正文。

## 运行环境

- Go `1.25` 或更高版本，必须安装在本机并可通过 `go version` 使用。
- Linux、WSL 或 macOS。
- 支持工具调用的 OpenAI 兼容模型或 Anthropic 模型。
- MCP 服务可选，用于扩展远程工具能力。

## 安装

### 安装 Go

请先安装 Go `1.25` 或更新版本，并确认命令可用：

```bash
go version
```

Go 官方安装说明见 <https://go.dev/doc/install>。Linux、WSL 或 macOS 用户完成安装后，应重新打开终端，确保 `go` 已加入 `PATH`。

### 构建并启动

```bash
git clone https://github.com/humb1e9/Pentgo.git
cd Pentgo
go build -o pentgo ./cmd/pentgo
./pentgo
```

首次启动会在当前目录创建 `.pentgo/` 工作区。源码仓库中的 `skills/` 目录需要保留在启动目录下，供 `/load_skill` 读取。

## 配置模型

配置文件路径：

| 系统 | 路径 |
| --- | --- |
| Linux / WSL | `${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json` |
| macOS | `$HOME/Library/Application Support/pentgo/config.json` |

推荐使用环境变量保存 API Key：

```bash
export OPENAI_API_KEY="TOKEN"
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/pentgo"
```

最小 OpenAI 兼容配置：

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

使用 Anthropic 时，将 `provider` 改为 `anthropic`，并在 `agent.anthropic` 中填写对应的 `base_url`、`model` 和 `api_key_env`。其他 OpenAI 兼容服务只需替换 `base_url` 与 `model`。

## 启动与会话

```text
pentgo
pentgo resume
```

`pentgo` 会打开当前目录并创建一个新会话；首次运行时会创建 `.pentgo/` 工作区。`pentgo resume` 只打开已有工作区，在进入终端前列出现有会话供选择，不会创建新会话。

## 交互命令

| 命令 | 说明 |
| --- | --- |
| `/load_skill` | 读取技能目录并加载名称与摘要。 |
| `/new` | 创建并进入新的空白会话。 |
| `/session rename NAME` | 重命名当前会话。 |
| `/session list` | 列出当前项目的会话。 |
| `/session delete [SESSION_ID]` | 删除指定会话；省略 ID 时删除当前会话。 |
| `/status` | 显示当前会话状态。 |
| `/blackboard` | 显示项目级共享记录。 |
| `/clear` | 清除当前终端的临时显示内容。 |
| `/help` | 显示命令摘要。 |
| `/exit` | 退出终端。 |

## 工作区数据

PentGo 以启动目录作为工作区边界，并在其中创建 `.pentgo/`：

```text
<working-directory>/
└── .pentgo/
    ├── pentgo.db
    └── tmp/
```

`pentgo.db` 保存项目、会话、对话、共享记录和工具执行记录。请将 `.pentgo/` 视为工作数据目录；删除该目录会删除当前目录对应的历史状态。

## MCP 工具服务

在 `agent.mcp` 下添加具名服务即可扩展远程工具。每个服务使用 `command` 时默认采用 stdio；也可使用 `type: "http"` 或 `type: "sse"` 配置远程端点。

```json
{
  "agent": {
    "mcp": {
      "files": {
        "command": "/absolute/path/to/tool-server",
        "args": ["--stdio"]
      },
      "catalog": {
        "type": "http",
        "url": "http://127.0.0.1:8080/mcp",
        "headers": {
          "Authorization": "Bearer TOKEN"
        }
      }
    }
  }
}
```

启动时会发现各服务公开的工具及参数定义。不同服务中的工具名称必须唯一。

## Skills

程序从启动目录的 `skills/*.md` 读取技能文件。执行 `/load_skill` 后，系统先提供技能名称和摘要，Agent 在需要时再读取指定文件的正文。

## 常见问题

### 找不到 `go` 命令

请安装 Go `1.25` 或更新版本，并重新打开终端后执行 `go version` 确认安装和 `PATH` 配置正确。

### 模型连接失败

检查 `provider`、`base_url`、`model` 和 API Key 环境变量名称是否与所使用的服务一致。

## 技术文档

- [技术文档](docs/TECHNICAL.md) 说明模块职责、运行时模型和持久化实现。
- [架构地图](docs/ARCHITECTURE.md) 说明目录结构与依赖方向。
