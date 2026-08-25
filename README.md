# PentGo

## 介绍

PentGo 是一个运行在终端里的持久化 AI Agent 工作台。它让你在一个目录中与模型连续协作：模型可以读写工作区文件、运行命令、调用你接入的工具，并在退出后恢复会话和项目记录。对话、工具结果与项目共享事实全部保存在本地 SQLite；上下文超限时自动压缩摘要，长任务可以跨多次启动继续推进。

> **仅限已获得明确授权的目标、数据和环境。** 请在开始前确认测试范围、账户权限和数据处理要求。

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

准备环境：

- 安装 Go **1.25 或更高版本**，并确认 `go version` 可运行。
- 准备支持工具调用的 OpenAI 兼容模型或 Anthropic 模型。

```bash
git clone https://github.com/humb1e9/Pentgo.git
cd Pentgo
./install.sh
```

`install.sh` 会依次完成：检查 Go 工具链（缺失时给出安装指引）、`go mod tidy` 拉齐依赖、编译 `pentgo` 到 `~/.local/bin`（无需 sudo）、首次安装时把内置 skills 复制到 `~/.local/share/pentgo/skills`（重复运行不覆盖）。确认 `~/.local/bin` 在 `PATH` 中即可运行 `pentgo`。

### 内置安全工具

PentGo 的侦察类 skills 依赖以下七个外部 CLI。请按表格自行安装并确认各命令在 `PATH` 中可运行：

| 工具 | 用途 | Kali/Debian (apt) | 备选安装 |
| --- | --- | --- | --- |
| amass | 子域名枚举 | `sudo apt install amass` | `go install github.com/owasp-amass/amass/v4/cmd/amass@latest` |
| subfinder | 子域名发现 | `sudo apt install subfinder` | `go install github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest` |
| gau | 从 Wayback/Common Crawl 抓取历史 URL | （apt 无此包） | `go install github.com/lc/gau/v2/cmd/gau@latest` |
| paramspider | 从 Web 归档挖掘参数 | `sudo apt install paramspider` | `pipx install paramspider` |
| katana | 爬虫与端点发现 | （apt 无此包） | `go install github.com/projectdiscovery/katana/cmd/katana@latest` |
| httpx | HTTP 存活探测 | `sudo apt install httpx-toolkit` | `go install github.com/projectdiscovery/httpx/cmd/httpx@latest` |
| wafw00f | WAF 指纹识别 | `sudo apt install wafw00f` | `pipx install wafw00f` |

注意：

- Debian/Kali 上 projectdiscovery httpx 的 apt 包名是 `httpx-toolkit`（`httpx` 包是另一个 Python HTTP 客户端）。
- `go install` 的产物在 `~/go/bin`，`pipx` 的产物在 `~/.local/bin`，请把对应目录加入 `PATH`。
- PentGo 不代理、不校验这些工具的行为；请仅在已获授权的目标上使用。要把它们暴露给模型，把命令登记到 `agent.local_tools`（见[本机 CLI](#本机-cli)）。

### 启动

在你要作为项目工作区的目录中运行：

```bash
pentgo
```

首次运行会创建新会话和 `.pentgo/` 数据目录。恢复已有项目并在启动时选择会话：

```bash
pentgo resume
```

### 常用终端命令

| 命令 | 作用 |
| --- | --- |
| `/new` | 新建会话。 |
| `/session list` | 查看会话。 |
| `/session rename 名称` | 重命名当前会话。 |
| `/session delete [会话 ID]` | 删除指定会话；不填 ID 时删除当前会话。 |
| `/status` | 查看当前会话状态。 |
| `/facts` | 查看有界的项目 Fact Index。 |
| `/clear` | 清除当前终端显示。 |
| `/help` | 查看命令帮助。 |
| `/exit` | 退出。 |

## 配置

配置文件位置：`${XDG_CONFIG_HOME:-$HOME/.config}/pentgo/config.json`。

### 模型

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

PentGo 会在每个 turn 开始时生成一次固定 4,096 Unicode runes、按 key 排序的 Fact Index 快照，并在该 turn 的所有模型请求中复用；同 turn 内写入的事实从下一 turn 才可见。Fact Index 包含 key、value 与可选来源编号，模型可用项目事实工具读取完整记录或被省略的条目。完整的原始对话记录与 Evidence 审计数据仍保存在本地；`context_window` 仅控制 Context Surface 压缩，不控制 Fact Index 是否注入。

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

### 工作区与 Skills

PentGo 将项目数据保存在启动目录中：

```text
你的工作目录/
└── .pentgo/
    ├── pentgo.db      # 会话、对话、上下文投影、证据、项目事实
    └── tmp/
```

请保留 `.pentgo/`，否则会丢失该目录的会话和项目记录。若要添加可复用的操作说明，在启动目录创建 `skills/` 并放入带 YAML frontmatter `description` 的 Markdown 文件；修改 skills 后重启 PentGo。

### 项目事实账本

项目范围的事实由 host 执行的三个工具维护：`upsert_project_fact`、`get_project_fact` 与 `list_project_facts`。记录只有小写 snake_case key、完整 value、可选 `evidence_ref` 和更新时间；key 必须匹配 `^[a-z][a-z0-9_]{0,63}$`，value 最多 16,384 Unicode runes。`evidence_ref` 只要求引用本项目现存的 Evidence（成功或失败均可）。同 key 的 upsert 完整覆盖 value 与来源；未提供 ref 会清除旧关联。项目是短期测试工作区，因此不提供删除、弃用、类别或图边。

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

依赖方向：`bootstrap` 是唯一组装根；`project` 根只保留 `ProjectStore`、`ProjectFact` 与 `OpenSQLite`（唯一 SQLite 连接入口），各子包拥有自己的 store（会话消息在 `session`、上下文投影在 `context`、证据在 `turn`），`session` 与 `context` 不反向依赖 `project` 根。

## 基础体验示例

以下是一次典型的授权侦察任务流程（输出为示意）：

```text
$ mkdir pentgo-demo && cd pentgo-demo
$ pentgo

  PentGo · 新会话已创建

› 对已授权目标 example.com 做被动子域名收集，存活探测后把结果写入项目事实

  ⋯ subfinder   {"args":["-d","example.com"]}
  ⋯ amass      {"args":["enum","-passive","-d","example.com"]}
  ⋯ httpx      {"args":["-l","/tmp/subs.txt","-silent"]}
  ⋯ upsert_project_fact  key: subdomains_alive

  完成：发现 23 个子域名，其中 17 个存活；清单与来源证据已写入项目事实
  subdomains_alive（evidence #3）。

› /facts
  subdomains_alive = api.example.com, dev.example.com, …（来源 #3）

› /exit
```

下次继续工作时：

```text
$ cd pentgo-demo
$ pentgo resume
```

选择上次的会话后，完整对话历史、项目事实与证据记录全部恢复；上下文过长时旧消息自动压缩为 checkpoint 摘要，任务可以接着推进。

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
