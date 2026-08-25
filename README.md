# PentGo

## 介绍

PentGo 是一个运行在终端里的持久化 AI Agent 工作台。它让你在一个目录中与模型连续协作：模型可以读写工作区文件、运行命令、调用你接入的工具，并在退出后恢复会话和项目记录。

> **仅限已获得明确授权的目标、数据和环境。**

## 特性速览

- **持久化会话**：在终端中创建、切换、恢复多个会话；退出后用 `pentgo resume` 继续。
- **工作区工具**：模型内置 `ls` / `read_file` / `write_file` / `edit_file` / `glob` / `grep` / `execute` 七个工作区工具，直接操作启动目录下的文件与命令。
- **本机 CLI 接入**：把 amass、subfinder 等任意命令登记为模型可调用的工具（`agent.local_tools`）。
- **标准 MCP**：支持 stdio / HTTP / SSE 三种 MCP 服务，与本机工具统一命名空间。
- **上下文管理**：Context Surface 持久化投影，超限时自动生成 checkpoint 摘要压缩前缀，配合 token 计量。
- **项目事实账本**：跨会话共享的项目事实，每 turn 注入有界 Fact Index 快照，模型可读可写。
- **Evidence 审计**：每次工具调用的完整记录（成功与失败均保留）持久化，可回溯。
- **Markdown Skills**：把常用流程写成带 frontmatter 的 Markdown，模型按需加载。

## 基础使用

### 安装

- Go 1.25+
- 支持工具调用的模型
- 七个内置安全工具

```bash
git clone https://github.com/humb1e9/Pentgo.git
cd Pentgo
go mod tidy
mkdir -p ~/.local/bin
go build -o ~/.local/bin/pentgo ./cmd/pentgo
data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/pentgo"
mkdir -p "$data_dir"
[ -d "$data_dir/skills" ] || cp -R skills "$data_dir/skills"
```

确认 `~/.local/bin` 在 `PATH` 中。

#### 七个内置安全工具

```bash
sudo apt update
sudo apt install -y amass subfinder paramspider httpx-toolkit wafw00f
go install github.com/lc/gau/v2/cmd/gau@latest
go install github.com/projectdiscovery/katana/cmd/katana@latest
export PATH="$HOME/go/bin:$PATH"
```

| 工具 | 用途 | apt | 备选 |
| --- | --- | --- | --- |
| amass | 子域名枚举 | `sudo apt install amass` | `go install github.com/owasp-amass/amass/v4/cmd/amass@latest` |
| subfinder | 子域名发现 | `sudo apt install subfinder` | `go install github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest` |
| gau | 历史 URL 抓取 | （apt 无此包） | `go install github.com/lc/gau/v2/cmd/gau@latest` |
| paramspider | 参数挖掘 | `sudo apt install paramspider` | `pipx install paramspider` |
| katana | 爬虫与端点发现 | （apt 无此包） | `go install github.com/projectdiscovery/katana/cmd/katana@latest` |
| httpx | HTTP 存活探测 | `sudo apt install httpx-toolkit` | `go install github.com/projectdiscovery/httpx/cmd/httpx@latest` |
| wafw00f | WAF 指纹识别 | `sudo apt install wafw00f` | `pipx install wafw00f` |

注意：Debian/Kali 上 httpx 的包名是 `httpx-toolkit`（`httpx` 是另一个工具）。`go install` 产物在 `~/go/bin`，需加入 `PATH`。

### 启动

```bash
pentgo        # 新会话
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

配置文件：`${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json`。

### 模型

```bash
export OPENAI_API_KEY="你的密钥"
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/pentgo"
```

```json
{
  "agent": {
    "provider": "openai",
    "max_turns": 20,
    "context": { "context_window": 128000 },
    "openai": {
      "base_url": "https://api.openai.com/v1",
      "model": "你的模型名",
      "api_key_env": "OPENAI_API_KEY"
    }
  }
}
```

Anthropic 将 `provider` 改为 `anthropic`，在 `agent.anthropic` 中填写 `base_url`、`model`、`api_key_env`。其他兼容服务只需替换 `base_url` 和 `model`。

### 本机 CLI

```json
{
  "agent": {
    "local_tools": {
      "amass": { "command": "amass", "description": "对已获授权的域名运行 Amass。" },
      "custom_recon": { "command": "/opt/tools/custom-recon" }
    }
  }
}
```

键名是模型看到的工具名。每个工具接收 `{"args":[...]}`，不经过 shell。工具名不能与内置工具冲突。

### MCP 服务

```json
{
  "agent": {
    "mcp": {
      "scanner": { "command": "my-mcp-server", "args": ["--stdio"] },
      "catalog": { "type": "http", "url": "http://127.0.0.1:8080/mcp" }
    }
  }
}
```

支持 stdio、HTTP、SSE。所有来源的工具名必须唯一。

### 工作区与 Skills

```text
工作目录/
└── .pentgo/
    ├── pentgo.db
    └── tmp/
```

保留 `.pentgo/`。在启动目录创建 `skills/`，放入带 YAML frontmatter `description` 的 Markdown 文件即可作为可复用技能。

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
docs/                  ARCHITECTURE.md、TECHNICAL.md
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

## 更多文档

- [技术文档](docs/TECHNICAL.md)
- [架构地图](docs/ARCHITECTURE.md)