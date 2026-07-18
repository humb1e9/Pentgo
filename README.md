# PentGo

PentGo 是一个终端 Agent Runtime。启动后输入包含 HTTP(S) URL 或域名的自然语言任务；模型以普通文本返回 Python 或 Bash 代码块，Runtime 在 engagement 专属工作目录中执行代码、保存每个块的 evidence，并把执行结果文本回灌给模型继续决策。

运行时没有固定的 Recon、Scan 或 Verify 阶段，也没有预定义命令调用目录。任务的后续步骤由模型根据已回灌的真实 stdout、stderr、退出码和 evidence 路径决定。

**读代码 / 改功能先看目录地图：** [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)（本地 `docs/` 默认 gitignore，克隆后若缺失可向维护者索取）。

## 运行

```bash
go build -o bin/pentgo ./cmd/pentgo
./bin/pentgo
```

```text
pentgo> 检查 https://TARGET 的公开 Web 行为并记录可验证证据
```

REPL 从输入中提取第一个 HTTP(S) URL 或裸域名；裸域名自动补为 `https://`。完整原文保存为 engagement intent。

内建命令：

```text
/help
/status
/cancel
/quit
/exit
```

一次只运行一个 engagement。`/cancel`、`Ctrl+C`、`/quit` 和 `/exit` 会终止当前代码进程并发布已生成的 artifact。

## 配置

配置文件位置：

| 环境 | 路径 |
| --- | --- |
| Linux / WSL | `${XDG_CONFIG_HOME:-~/.config}/pentgo/config.json` |
| macOS | `~/Library/Application Support/pentgo/config.json` |

`config.json` 含凭据，仓库根目录已在 `.gitignore` 中排除它。配置使用根级 `agent`：

```json
{
  "agent": {
    "provider": "openai",
    "max_turns": 20,
    "request_timeout_seconds": 60,
    "execution_timeout_seconds": 1800,
    "max_output_bytes": 65536,
    "max_parallel_blocks": 4,
    "no_code_limit": 3,
    "provider_retry_delay_seconds": 3,
    "network_backoff_seconds": 15,
    "soft_stuck_turns": 3,
    "hard_stuck_turns": 5,
    "line_repeat_limit": 100,
    "scan_line_repeat_limit": 500,
    "openai": {
      "base_url": "https://api.deepseek.com",
      "model": "MODEL",
      "api_key": "YOUR_KEY",
      "thinking_mode": "disabled"
    },
    "anthropic": {
      "base_url": "https://api.anthropic.com",
      "model": "MODEL",
      "api_key_env": "ANTHROPIC_API_KEY"
    },
    "authorization": {
      "enabled": true,
      "allow_destructive": false,
      "allow_private_hosts": true,
      "allowed_hosts": []
    }
  }
}
```

OpenAI 兼容 Provider 使用 Chat Completions 普通文本消息，不发送 native tool definitions。`thinking_mode` 非空时会作为 OpenAI 兼容接口的 `thinking` 字段发送。

授权门会在执行前校验代码块：默认仅允许输入目标主机及其子域，拦截破坏性 SQL 与系统命令；`allowed_hosts` 可追加额外授权主机。范围检查只静态解析代码中直接出现的 HTTP(S) URL，并非沙箱；变量拼接或其他间接访问形式可能绕过该检查，因此它仅是纵深防御的一层。

## 执行模型

每轮模型回复中的全部下列 fenced code block 都会按源码顺序收集：

| Fence | 解释器 |
| --- | --- |
| `python`、`python3` | `python3 -u` |
| `bash`、`sh`、`shell` | `bash` |

同一轮的块最多并发 `max_parallel_blocks` 个；每块在 `work/` 内保留源文件，拥有 `PENTGO_TARGET`、`PENTGO_ENGAGEMENT_ID` 和 `PENTGO_WORKDIR` 环境变量。Python 在执行前会进行语法、JSON、空实现、占位符检查，并对少量缺失 import 或 HTTP timeout 生成可审计修复副本。Shell 代码不使用命令白名单。

Runtime 会回灌每块的状态、退出码、stdout、stderr 和 evidence 路径，并处理无代码回复、无证据声明、预检拒绝、无输出、Provider 单次重试、网络阻塞、重复模型回复和重复输出行。

模型可以单独输出：

```text
SKILL_LOAD: terminal
```

以加载注册的本地 Markdown 知识。Skill 只是只读上下文，不是命令适配器。

## 产物

每个 engagement 发布到启动 REPL 时的当前目录：

```text
eng-<timestamp>-<random>/
├── evidence/
│   └── agent-turn-001-block-001.json
├── work/
│   └── turn-001-block-001.py
├── session.json
└── report.md
```

- `session.json` 保存会话状态、任务、加载 Skill、时间线和终止原因。
- `evidence/*.json` 保存每个代码块的原始/修复代码、解释器、输出、退出状态和截断信息。
- `work/` 保存跨轮产生的脚本和文件。
- `report.md` 由收尾模型调用基于有界执行摘要和 evidence 路径生成中文 Markdown 报告；该调用不执行代码、不复用完整聊天历史。模型调用失败、返回空文本或任务已取消时，`report.md` 自动回退为确定性执行时间线。

## 开发验证

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
go mod verify
```
