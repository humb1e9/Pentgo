# PentGo

## 介绍

PentGo 是一个运行在终端里的持久化 AI Agent 工作台。它让你在一个目录中与模型连续协作：模型可以读写工作区文件、运行命令、调用你接入的工具，并在退出后恢复会话和项目记录。

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
go install ./cmd/pentgo

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

编辑 `${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json`，至少填写 `model.model` 和 `model.api_key`；随后重新启动：

```bash
pentgo        # 创建新会话
pentgo resume # 恢复已有项目并选择会话
```

### 常用终端命令

| 命令 | 作用 |
| --- | --- |
| `/new` | 新建会话 |
| `/session list` | 查看会话 |
| `/session rename 名称` | 重命名当前会话 |
| `/session delete [会话 ID]` | 删除指定/当前会话 |
| `/status` | 当前会话状态 |
| `/facts` | 查看 Fact Index |
| `/clear` | 清除终端显示 |
| `/help` | 帮助 |
| `/exit` | 退出 |

## 配置

配置文件：`${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json`。首次运行 `pentgo` 会自动生成 `0600` 模板，编辑其中的 `model.model` 与 `model.api_key` 即可。

```json
{
  "model": {
    "provider": "openai",
    "base_url": "https://api.openai.com/v1",
    "model": "你的模型名",
    "api_key": "你的密钥"
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
    "context": { "context_window": 128000 }
  }
}
```

- `model.provider` 决定协议（`openai` / `anthropic`），连接参数全部平铺在 `model` 下。Anthropic 只需把 `provider` 换成 `anthropic` 并替换 `base_url` 与 `model`；其他兼容服务通常只需替换 `base_url` 和 `model`。
- `tools.local` 把本机命令暴露为模型工具：键名是模型看到的工具名，`command` 支持 `PATH` 命令或绝对路径。工具接收 `{"args":[...]}` 原生参数数组，不经过 shell；工具名不能与内置工具冲突。
- `tools.mcp` 接入 stdio（`command`/`args`）或 HTTP/SSE（`type` + `url`）服务。所有来源的工具名必须唯一。
- `project.max_turns` 限制单个用户请求内的模型调用次数（默认 1000），`project.context` 控制上下文预算（`context_window` 等，全部有缺省值）。

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
cmd/pentgo/            进程入口：信号处理与依赖装配
internal/
├── bootstrap/         组装根：用户配置读取、路径解析、Application 装配
├── core/              共享协议：Message、Tool、ModelStepper 等值类型
├── model/             模型适配：OpenAI/Anthropic Eino 单步流、prompt
├── project/           项目域根：ProjectStore、ProjectFact、OpenSQLite、Open* 装配
│   ├── session/       会话：Session/Worker 实体与 ConversationStore
│   ├── context/       上下文：ContextAssembler/Compactor 与 ContextSurfaceStore
│   ├── turn/          单轮：turn 执行流、EvidenceStore、项目事实工具
│   └── runtime/       生命周期：Manager（工作区/项目/会话）、工具组合
├── terminal/          终端界面：bubbletea 视图、命令解析、事件渲染
└── tools/             工具实现：工作区工具、本机 CLI、MCP 客户端、skills
skills/                内置 Markdown skills（首次安装复制到用户数据目录）
```

依赖方向：`bootstrap` 是唯一组装根；`project` 根只保留 `ProjectStore`、`ProjectFact` 与 `OpenSQLite`，各子包拥有自己的 store（会话消息在 `session`、上下文投影在 `context`、证据在 `turn`），`session` 与 `context` 不反向依赖 `project` 根。

## 基础体验示例

```text
$ mkdir demo && cd demo
$ pentgo
› 对 example.com 做被动子域名收集
  ⋯ subfinder / amass → httpx → upsert_project_fact
  完成：23 子域名，17 存活，写入 subdomains_alive
› /facts
› /exit
$ pentgo resume   # 会话、事实、证据全部恢复
```
