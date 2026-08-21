# Bingo 风格终端 Agent 实施计划

> **执行方式：** 按测试驱动开发实现。完成每个步骤后运行该步骤列出的测试，再进入下一步。

**目标：** 以单一终端 Agent Runtime 替换 PentGo 的固定 `recon -> scan -> verify -> persist` 流水线。模型以普通文本生成 Python 或 Shell 代码块；运行时执行全部代码块、回灌证据文本、维护对话历史，并在无代码、无证据、网络阻塞、重复输出和模型调用失败时恢复执行。

**架构：** `cmd/pentgo` 保持为启动入口。`internal/terminal` 负责 REPL，`internal/app` 负责组装一次任务，新的 `internal/runtime` 承载会话、代码解析、预检、执行和模型循环，`internal/agent` 仅负责文本模型协议，`internal/report` 负责工作目录和报告发布，`skills` 仅提供按名称加载的只读提示词。删除 `internal/redteam`、`internal/skill` 及其测试镜像。

**技术栈：** Go 标准库、OpenAI 兼容 Chat Completions、Anthropic Messages、Python 3、POSIX Shell。

---

## 1. 收敛配置与模型文本协议

**文件：**
- 修改：`internal/config/config.go`
- 修改：`internal/agent/types.go`
- 修改：`internal/agent/openai.go`
- 修改：`internal/agent/anthropic.go`
- 修改：`tests/_packages/internal/config/config_test.go`
- 修改：`tests/_packages/internal/agent/client_test.go`

**步骤：**
1. 先为根级 `agent` 配置和运行时限制编写失败测试：供应商、模型、基础地址、超时、最大循环、最大并行块、历史窗口、单块超时、网络等待和输出上限均应可校验并具有默认值。
2. 将 `agent.Client` 收敛为普通文本 `Chat(ctx, Request) (Response, error)`；请求只包含系统提示词和消息历史，响应只包含模型正文。
3. 修改 OpenAI 兼容及 Anthropic 客户端，使其发送普通聊天消息，不传原生工具定义或工具调用字段。
4. 移除配置中的 `recon`、`scan`、Nuclei、Tscan 和固定阶段参数；保留配置文件不进入版本控制的既有约束。
5. 运行：`go test ./tests/_packages/internal/config ./tests/_packages/internal/agent`

## 2. 建立运行时会话、目标和历史模型

**文件：**
- 新增：`internal/runtime/target.go`
- 新增：`internal/runtime/session.go`
- 新增：`internal/runtime/history.go`
- 新增：`tests/packages/internal/runtime/target.go`
- 新增：`tests/packages/internal/runtime/session.go`
- 新增：`tests/packages/internal/runtime/history.go`
- 新增：`tests/_packages/internal/runtime/target_test.go`
- 新增：`tests/_packages/internal/runtime/session_test.go`
- 新增：`tests/_packages/internal/runtime/history_test.go`

**步骤：**
1. 为 URL 规范化、任务标识符、状态流转和历史裁剪写失败测试。
2. 实现 `Target`，只保存原始输入与规范化 URL，不再建立 recon 专有目标模型。
3. 实现 `AgentSession`，状态限定为 `pending`、`running`、`done`、`failed`、`cancelled`，记录轮次、任务、目标、时间线、代码块和运行摘要。
4. 实现 `History`：首条不可变消息为归一化任务上下文；后续依次写入 assistant 决策、执行结果和恢复提示；按消息数与字符数裁剪旧证据，不裁剪当前任务上下文。
5. 运行：`go test ./tests/_packages/internal/runtime -run 'Test(Target|Session|History)'`

## 3. 解析代码块并完成执行前预检

**文件：**
- 新增：`internal/runtime/blocks.go`
- 新增：`internal/runtime/preflight.go`
- 新增：`tests/packages/internal/runtime/blocks.go`
- 新增：`tests/packages/internal/runtime/preflight.go`
- 新增：`tests/_packages/internal/runtime/blocks_test.go`
- 新增：`tests/_packages/internal/runtime/preflight_test.go`

**步骤：**
1. 为多语言、多代码块、源码顺序、缺少结束围栏、忽略未知语言和保留原始代码写失败测试。
2. 实现对 `python`、`python3`、`bash`、`sh`、`shell` 围栏的提取；同一响应内的全部支持块均产生 `CodeBlock`。
3. 为 Python 预检写失败测试：语法错误、JSON 解析、空实现、占位符、Base64 解码、HTTP 超时补齐和原始/修复代码记录。
4. 实现 Python 预检及有限修复。不可修复内容返回结构化预检拒绝，拒绝文本可直接回灌模型。Shell 代码不走命令白名单。
5. 运行：`go test ./tests/_packages/internal/runtime -run 'Test(Extract|Preflight)'`

## 4. 实现隔离工作目录中的代码执行器

**文件：**
- 新增：`internal/runtime/executor.go`
- 新增：`tests/packages/internal/runtime/executor.go`
- 新增：`tests/_packages/internal/runtime/executor_test.go`

**步骤：**
1. 为 Python 和 Shell 执行、标准输出与错误输出、顺序化结果、并行限制、单块超时、进程组终止、退出码、输出截断及重复行检测写失败测试。
2. 实现 `Executor`：每个块写入会话工作目录，Python 经 `python3` 执行，Shell 经 `bash` 或 `sh` 执行，输出带时间戳和块序号。
3. 通过 POSIX 进程组处理超时与取消，确保子进程一并终止；限制每轮并行块数，最终结果按模型源码顺序返回。
4. 将块文件路径、原始/修复代码、命令、持续时间、退出状态、输出摘要及截断信息写入 `ExecutionResult`。
5. 运行：`go test ./tests/_packages/internal/runtime -run TestExecutor`

## 5. 将 Skill 收敛为只读模型上下文

**文件：**
- 修改：`skills/registry.go`
- 新增：`skills/terminal/SKILL.md`
- 修改：`tests/_packages/skills/registry_test.go`

**步骤：**
1. 为列出 Skill、读取 Skill、拒绝路径穿越、未知 Skill 及最大内容长度写失败测试。
2. 将现有 registry 改为按名称加载嵌入的 Markdown，保留 `recon`，新增 `terminal`；移除阶段专属 `PhasePrompt` API。
3. 在终端系统提示词中说明 `SKILL_LOAD("name")` 的返回格式，并由运行时识别该请求、写入历史后让模型继续。
4. 运行：`go test ./tests/_packages/skills`

## 6. 实现模型循环和 Bingo 式恢复策略

**文件：**
- 新增：`internal/runtime/runner.go`
- 新增：`internal/runtime/recovery.go`
- 新增：`internal/runtime/prompt.go`
- 新增：`tests/packages/internal/runtime/runner.go`
- 新增：`tests/packages/internal/runtime/recovery.go`
- 新增：`tests/packages/internal/runtime/prompt.go`
- 新增：`tests/_packages/internal/runtime/runner_test.go`
- 新增：`tests/_packages/internal/runtime/recovery_test.go`

**步骤：**
1. 用可编排的假模型和假执行器为完整闭环写失败测试：决策、全部代码块执行、结果回灌、完成标识、取消和最终状态。
2. 实现 `Runner.Run`：追加 assistant 决策，加载 Skill 或提取全部代码块，执行并将压缩后的证据作为用户消息追加，再发起下一轮模型调用。
3. 实现恢复规则及测试：连续三次无代码、无证据声明、预检拒绝、所有块无有效输出、模型请求重试一次、网络阻塞等待、响应指纹三次软恢复与五次硬停止、执行输出的重复行停止。
4. 识别 `TASK_COMPLETE`、`MISSION_COMPLETE`、`TARGET_FAILED`，只在带有已回灌证据的情况下完成或失败；循环上限和取消均写入明确终止原因。
5. 运行：`go test ./tests/_packages/internal/runtime`

## 7. 用通用运行时工件和报告取代阶段报告

**文件：**
- 修改：`internal/report/artifacts.go`
- 修改：`internal/report/markdown.go`
- 修改：`tests/_packages/internal/report/artifacts_test.go`
- 新增：`tests/_packages/internal/report/markdown_test.go`

**步骤：**
1. 为每会话工作目录、证据目录、代码文件、原子发布、失败保留、运行时间线和 Markdown 报告写失败测试。
2. 将工件写入器的输入从旧 `redteam.Session` 改为 `runtime.AgentSession`，创建 `work/` 与 `evidence/`，并将执行摘要和输出引用写入可发布目录。
3. 将 Markdown 报告改为任务、目标、状态、轮次、代码块、执行证据和停止原因，不再依赖 recon 或 scan 类型。
4. 运行：`go test ./tests/_packages/internal/report`

## 8. 连接 App 与终端 REPL

**文件：**
- 修改：`internal/app/engagement.go`
- 修改：`internal/terminal/terminal.go`
- 修改：`cmd/pentgo/main.go`
- 修改：`tests/_packages/internal/app/engagement_test.go`
- 新增：`tests/_packages/internal/terminal/terminal_test.go`

**步骤：**
1. 为自然语言任务启动、运行中状态、单任务互斥、即时取消、报告路径和 `/help` 写失败测试。
2. `app.Service` 创建运行时会话、工作区、模型客户端和 `Runner`，完成后发布报告；服务不再选择 recon、scan 或 verify 阶段。
3. REPL 接受自然语言作为任务，保留 `/help`、`/status`、`/cancel`、`/quit` 和 `/exit`；运行中只允许一个任务，取消信号传入 runner 的上下文。
4. 运行：`go test ./tests/_packages/internal/app ./tests/_packages/internal/terminal ./cmd/pentgo`

## 9. 删除固定流水线并做全量验证

**文件：**
- 删除：`internal/redteam/`
- 删除：`internal/skill/`
- 删除：`tests/packages/internal/redteam/`
- 删除：`tests/packages/internal/skill/`
- 删除：`tests/_packages/internal/redteam/`
- 删除：`tests/_packages/internal/skill/`
- 修改：受删除包影响的所有导入与文档引用

**步骤：**
1. 删除 recon、scan、verify、Tscan、Nuclei、候选、确认、旧阶段会话和命令 Skill 实现及测试镜像。
2. 使用 `rg` 确认生产代码中不存在 `internal/redteam`、`internal/skill`、`Nuclei`、`Tscan`、`ReconPipeline` 和固定阶段编排引用。
3. 运行 `gofmt`，再运行 `go test ./...`、`go vet ./...` 和 `git diff --check`。
4. 审核变更边界，确认没有删除用户的无关改动，并提交实现与测试。

