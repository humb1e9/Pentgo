# PentGo

PentGo 是一个运行在终端里的持久化 AI Agent 工作台。它让你在一个目录中与模型连续协作：模型可以读写工作区文件、运行命令、调用你接入的工具，并在退出后恢复会话和项目记录。

> **仅限已获得明确授权的目标、数据和环境。** 请在开始前确认测试范围、账户权限和数据处理要求。

## 你可以做什么

- 在终端中创建、切换和恢复多个会话。
- 让模型使用工作区内的文件工具和命令执行工具完成任务。
- 为模型接入本机 CLI，或接入标准 MCP 服务。
- 将对话、工具结果和项目共享记录保存在当前工作目录，之后继续工作。
- 将常用流程写成 Markdown skills，让模型按需加载。

## 快速开始

### 1. 准备环境

- 安装 Go **1.25 或更高版本**，并确认 `go version` 可运行。
- 准备支持工具调用的 OpenAI 兼容模型或 Anthropic 模型。

```bash
git clone https://github.com/humb1e9/Pentgo.git
cd Pentgo
go build -o pentgo ./cmd
```

### 2. 配置模型

配置文件位置：

| 系统 | 配置文件 |
| --- | --- |
| Linux / WSL | `${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json` |
| macOS | `$HOME/Library/Application Support/pentgo/config.json` |

以 OpenAI 兼容接口为例：

```bash
export OPENAI_API_KEY="你的密钥"
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/pentgo"
```

创建 `config.json`：

```json
{
  "agent": {
    "provider": "openai",
    "max_turns": 20,
    "context": {
      "context_window": 128000
    },
    "openai": {
      "base_url": "https://api.openai.com/v1",
      "model": "你的模型名",
      "api_key_env": "OPENAI_API_KEY"
    }
  }
}
```

使用 Anthropic 时，将 `provider` 改为 `anthropic`，并在 `agent.anthropic` 中填写 `base_url`、`model` 与 `api_key_env`。其他兼容 OpenAI 的服务通常只需替换 `base_url` 和 `model`。

当配置 `agent.context.context_window` 后，PentGo 会自动管理长会话上下文，优先保留近期工作与项目事实；完整项目审计数据仍保存在本地。省略该配置则保持完整 transcript 回放兼容行为。

### 3. 启动

在你要作为项目工作区的目录中运行：

```bash
/path/to/pentgo
```

首次运行会创建新会话和 `.pentgo/` 数据目录。恢复已有项目并在启动时选择会话：

```bash
/path/to/pentgo resume
```

## 常用终端命令

| 命令 | 作用 |
| --- | --- |
| `/new` | 新建会话。 |
| `/session list` | 查看会话。 |
| `/session rename 名称` | 重命名当前会话。 |
| `/session delete [会话 ID]` | 删除指定会话；不填 ID 时删除当前会话。 |
| `/status` | 查看当前会话状态。 |
| `/blackboard` | 查看项目共享记录。 |
| `/clear` | 清除当前终端显示。 |
| `/help` | 查看命令帮助。 |
| `/exit` | 退出。 |

## 添加工具

### 本机 CLI

把希望模型调用的普通命令放到 `agent.local_tools`。键名是模型看到的工具名；`command` 可以是 `PATH` 中的命令名，也可以是绝对路径。

```json
{
  "agent": {
    "local_tools": {
      "amass": {
        "command": "amass",
        "description": "对已获授权的域名运行 Amass。"
      },
      "custom_recon": {
        "command": "/opt/tools/custom-recon",
        "description": "运行团队自定义的授权资产收集命令。"
      }
    }
  }
}
```

PentGo 在模型调用时直接运行该命令。每个工具接收一个原生参数数组，例如 `{"args":["-d","example.com"]}`。请自行确保命令已安装、在 `PATH` 中可用或使用正确的绝对路径；若修改了 `PATH`，请重启 PentGo。工具名需唯一，且不能使用 PentGo 内置工具名称。

### MCP 服务

实际提供 MCP 协议的服务配置在 `agent.mcp`，而不是 `local_tools`：

```json
{
  "agent": {
    "mcp": {
      "scanner": {
        "command": "my-mcp-server",
        "args": ["--stdio"]
      },
      "catalog": {
        "type": "http",
        "url": "http://127.0.0.1:8080/mcp"
      }
    }
  }
}
```

支持 stdio、HTTP 和 SSE MCP 服务。所有来源的工具名称必须唯一。

## 工作区与 Skills

PentGo 将项目数据保存在启动目录中：

```text
你的工作目录/
└── .pentgo/
    ├── pentgo.db
    └── tmp/
```

请保留 `.pentgo/`，否则会丢失该目录的会话和项目记录。若要添加可复用的操作说明，在启动目录创建 `skills/` 并放入带 YAML frontmatter `description` 的 Markdown 文件；修改 skills 后重启 PentGo。

## 常见问题

### 找不到命令或本机工具无法运行

确认该 CLI 已安装且 `PATH` 正确，或在 `local_tools` 中改用绝对路径。修改环境变量后请重新启动 PentGo。

### 模型连接失败

检查 `provider`、`base_url`、`model` 与 API Key 环境变量是否匹配所使用的服务。

### 想传递服务商专属模型参数

使用 `agent.openai.request_extra` 原样传递服务商要求的字段；高级模型响应字段映射请参阅技术文档。

## 更多文档

- [技术文档](docs/TECHNICAL.md)：实现边界、运行时模型、工具协议和开发验证。
- [架构地图](docs/ARCHITECTURE.md)：目录结构与依赖方向。
