# PentGo

PentGo 是一个终端 Agent Runtime。启动后输入包含 HTTP(S) URL 或域名的自然语言任务；一个 Eino 工具调用 Agent 在 engagement 专属工作目录中执行动作，并将动作结果追加到单一 `evidence.jsonl`。

运行时没有固定的 Recon、Scan 或 Verify 阶段。任务的后续步骤由模型根据已回灌的真实 stdout、stderr、退出码和 `[evidence_ref: N]` 决定。

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
    "network_backoff_seconds": 15,
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
    },
    "mcp": {
      "command": "/absolute/path/to/local-mcp-server",
      "args": ["--stdio"],
      "env": {"FIXTURE_TOKEN": "TOKEN"}
    }
  }
}
```

OpenAI 与 Anthropic provider 都通过 Eino 的原生 tool-call 模型驱动同一 Agent 循环。

授权门会在执行前校验代码块：默认仅允许输入目标主机及其子域，拦截破坏性 SQL 与系统命令；`allowed_hosts` 可追加额外授权主机。范围检查只静态解析代码中直接出现的 HTTP(S) URL，并非沙箱；变量拼接或其他间接访问形式可能绕过该检查，因此它仅是纵深防御的一层。

## 执行模型

Agent 可调用 `exec(command)`、`execute_python(script)`、`load_skill(name)` 和 `record_finding(...)`。前两个动作经预检、授权和本地执行后追加一条 JSONL 证据记录；`load_skill` 与 `record_finding` 不写 Journal。每项 finding 必须引用成功动作的 evidence sequence。首个无工具调用的普通助手回复自然结束 engagement。

`agent.mcp` 可选配置一个本地 stdio MCP server。每次 engagement 启动一个子进程、发现工具并按原始 MCP 工具名挂到 Agent；动作结果与本地执行共用 Evidence Journal，发布前关闭 MCP 子进程。只支持一个本地 stdio server，不提供多服务器、HTTP/SSE、重连或管理层。

运行时脚本使用 `bash` 或 `python3 -u`，拥有 `PENTGO_TARGET`、`PENTGO_ENGAGEMENT_ID` 和 `PENTGO_WORKDIR` 环境变量。Python 在执行前进行语法、JSON、空实现、占位符检查，并对少量缺失 import 或 HTTP timeout 生成修复副本。

## 产物

每个 engagement 发布到启动 REPL 时的当前目录：

```text
eng-<timestamp>-<random>/
├── evidence.jsonl
├── work/
│   └── artifact.txt
├── session.json
└── report.md
```

- `session.json` 保存会话状态、任务、finding、最终摘要和终止原因。
- `evidence.jsonl` 以完成顺序保存 `exec`、`execute_python` 及已发现的 MCP 工具结果。
- `work/` 保存跨轮产生的脚本和文件。
- `report.md` 在本地按 `session.json` 的确定性结构渲染。

## 开发验证

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
go mod verify
```
