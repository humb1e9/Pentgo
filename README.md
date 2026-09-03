# PentGo

## 介绍

PentGo 是一个运行在终端中的持久化渗透测试 AI Agent。用户以自然语言描述已授权的测试目标和功能点后，模型结合匹配的 Skill、当前可用工具和会话上下文，制定分阶段的测试步骤并根据工具结果持续调整策略。

PentGo 统一接入工作区能力、本地 CLI 和 MCP 工具。每次工具调用结果会先保存为带 `evidence_ref` 的证据，再反馈给模型继续分析；会话、轮次、消息、项目事实、证据、上下文摘要和执行 checkpoint 都持久化在项目 SQLite 数据库中，因此任务可以暂停、恢复和复核。

对于“对某站点进行渗透测试”这类范围过大的请求，模型会先推荐可选测试方向、说明前置条件和风险，等待用户明确测试范围；对于 SQL 注入、登录绕过等功能点明确的请求，模型会围绕该目标执行低风险、可复核的验证流程。

> **仅限已获得明确授权的目标、数据和环境。**

## 基础使用

### 安装

**前置条件：**Linux/macOS、Git、Go 1.25 或更高版本，以及一个支持工具调用的模型服务。以下命令在 Debian/Kali 的 Bash 中可直接执行；其他发行版请用对应包管理器安装 Go 和 Git。

#### 1. 下载、编译并安装 PentGo

```bash
# 首次安装
git clone https://github.com/humb1e9/Pentgo.git
cd Pentgo

# 编译并安装到 $(go env GOPATH)/bin，无需 sudo
go install ./cmd

# 首次安装时复制内置 Skills；已有个人 Skills 不会被覆盖
# PentGo 固定从 ~/.local/share/pentgo/skills 读取 Skills。
skills_dir="$HOME/.local/share/pentgo/skills"
install -d "$(dirname "$skills_dir")"
[ -d "$skills_dir" ] || cp -R skills "$skills_dir"
```

将 `go install` 安装的 PentGo 和其他工具永久加入 `PATH`。按你正在使用的 Shell **二选一**执行：

```bash
# Bash
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

```bash
# Zsh
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

#### 2. 可选：安装常用侦察工具

PentGo 可以直接运行而不安装这些工具；安装后，模型才可通过已配置的本机工具调用它们。

```bash
sudo apt update
sudo apt install -y amass subfinder paramspider httpx-toolkit wafw00f
go install github.com/lc/gau/v2/cmd/gau@latest
go install github.com/projectdiscovery/katana/cmd/katana@latest
```

Debian/Kali 上 HTTPX 的包名是 `httpx-toolkit`；`httpx` 是另一个软件包。

### 首次启动与配置

在希望保存项目记录的工作目录启动。首次运行会创建配置模板并退出：

```bash
mkdir -p ~/pentgo-workspace
cd ~/pentgo-workspace
pentgo
```

编辑 `~/.config/pentgo/config.json`，至少填写 `model.model` 和 `model.api_key`；随后重新启动：

```bash
pentgo        # 创建新会话
pentgo resume # 恢复已有项目并选择会话
```

### 常用终端命令

| 命令 | 作用 |
| --- | --- |
| `/new` | 新建会话 |
| `/session list` | 查看会话 |
| `/session 会话ID` | 切换到指定会话 |
| `/session delete [会话 ID]` | 删除指定/当前会话 |
| `/help` | 帮助 |
| `Ctrl+C` | 退出 |

## 配置

配置文件：`~/.config/pentgo/config.json`。首次运行 `pentgo` 会自动生成 `0600` 模板，编辑其中的 `model.model` 与 `model.api_key` 即可。

```json
{
  "model": {
    "provider": "openai",
    "base_url": "https://api.openai.com/v1",
    "model": "你的模型名",
    "api_key": "你的密钥",
    "thinking_effort": "high"
  },
  "tools": {
    "max_output_bytes": 65536,
    "local": {
      "amass": { "command": "amass", "description": "对已获授权的域名运行 Amass。" },
      "custom_recon": { "command": "/opt/tools/custom-recon" }
    },
    "mcp": {
      "scanner": { "command": "my-mcp-server", "args": ["--stdio"] },
      "catalog": { "type": "http", "url": "http://127.0.0.1:8080/mcp" }
    }
  },
  "project": {
    "max_turns": 1000,
    "context": { "context_window": 262144, "recent_messages": 32, "summary_max_tokens": 8192 }
  }
}
```

- `model.provider` 决定协议（`openai` / `anthropic`），连接参数全部平铺在 `model` 下。Anthropic 只需把 `provider` 换成 `anthropic` 并替换 `base_url` 与 `model`；其他兼容服务通常只需替换 `base_url` 和 `model`。
- `model.thinking_effort` 可设为 `low`、`medium` 或 `high`；非空时 OpenAI 兼容请求会带上 `enable_thinking: true`，并把它作为 `reasoning_effort` 发送，留空（默认）则关闭思考输出。仅在所选网关/模型支持思考输出时设置。
- `tools.local` 把本机命令暴露为模型工具：键名是模型看到的工具名，`command` 支持 `PATH` 命令或绝对路径。工具接收 `{"args":[...]}` 原生参数数组，不经过 shell；工具名不能与内置工具冲突。
- `tools.mcp` 接入 stdio（`command`/`args`）或 HTTP/SSE（`type` + `url`）服务。所有来源的工具名必须唯一。
- `project.max_turns` 限制单个用户请求内的模型调用次数（默认 1000）；`project.context.context_window` 是实际模型请求的总输入 token 预算（默认 256000），包含 instruction、工具 schema、Facts、摘要和原始消息；`recent_messages` 保留最近原始消息的最大条数（默认 32），`summary_max_tokens` 限制滚动摘要最大输出（默认 8192）。

### 工作区与 Skills

```text
工作目录/
└── .pentgo/
    ├── pentgo.db
    └── tmp/
```

保留 `.pentgo/`。可复用 Skills 位于 `~/.local/share/pentgo/skills/`；在其中放入带 YAML frontmatter `description` 的 Markdown 文件即可加载。

### 项目事实账本

三个 host 工具：`upsert_project_fact`、`get_project_fact`、`list_project_facts`。key 为 `^[a-z][a-z0-9_]{0,63}$`，value 最多 16,384 runes，可选 `evidence_ref`。同 key 的 upsert 完整覆盖。

## 项目结构

```text
cmd/                    进程入口
app/                    启动、配置、路径与依赖装配
terminal/               Bubble Tea 终端界面
internal/
├── model/              模型配置、Eino Provider 与 Prompt
├── agent/              Agent 编排、轮次、事件、工具适配、暂停与恢复
├── context/            token 预算、历史摘要与项目事实索引
├── tools/              工作区工具、本地 CLI、MCP 客户端与 Skill Registry
├── evidence/           工具结果证据记录、查询与输出脱敏
├── session/            会话、轮次、消息、事件与 ConversationStore
├── storage/            SQLite、ProjectStore、checkpoint、事实与摘要持久化
└── project/            项目、配置、项目事实等领域对象
skills/                 内置 Markdown Skills（首次安装复制到用户数据目录）
```

依赖方向：`cmd → app → agent`；`terminal → app/controller`；`agent → model/context/tools/evidence/session/storage/project`；`storage → project`。`model`、`tools` 和 `session` 分别拥有各自的核心契约；`agent` 负责组合与编排，不直接拥有 SQLite、MCP 或终端实现。

## 基础体验示例

```text
$ mkdir demo && cd demo
$ pentgo
› 对 example.com 做被动子域名收集
  ⋯ subfinder / amass → httpx → upsert_project_fact
  完成：23 子域名，17 存活，写入 subdomains_alive
› /session list
› 按 Ctrl+C 退出
$ pentgo resume   # 会话、事实、证据全部恢复
```
