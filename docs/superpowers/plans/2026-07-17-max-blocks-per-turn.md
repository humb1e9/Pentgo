# Max Blocks Per Turn Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增"单轮代码块数量上限" `max_blocks_per_turn`（默认 8，参照 bingo `ParallelRunner` 的 worker 上限），防止模型单轮甩出几十个块猛打目标；超限时执行前 N 个并回灌提醒，保留进度而非整轮拒绝。

**Architecture:** `max_parallel_blocks` 控制**并发**、`max_blocks_per_turn` 控制单轮**总数**，二者互补。Runner 在提取代码块后、preflight 之前，若块数超限则截断到前 N 个并追加 `TOO MANY BLOCKS` 用户消息与 `too_many_blocks` 恢复事件，复用现有历史/事件通路。

**Tech Stack:** Go 1.25 标准库；沿用现有 `config`、`runtime`、`app` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `internal/<pkg>/`；测试真身放 `tests/_packages/internal/<pkg>/`，源码目录建相对符号链接（与现有一致）。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- `max_blocks_per_turn` 是**限速安全护栏**：配置默认 8，`normalize` 把 `<=0` 归一为 8（不可意外关闭）；Runner 层保留 `limit>0` 判定以便测试注入 0 表示不限。
- 截断只影响执行数量，不改动 preflight、授权门、执行器、恢复策略及其现有测试断言。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

## 参考依据

bingo 主终端循环顺序执行全部块、无显式上限；其唯一并发上限在 `bingo/core/parallel_runner.py` 的 `ParallelRunner`，`max_workers` clamp 到 `[1,8]`。故取 **8** 作为单轮块数量默认上限。

---

### Task 1: 配置项 max_blocks_per_turn

**Files:**
- Modify: `internal/config/config.go`
- Test: `tests/_packages/internal/config/config_test.go` (已存在，追加/改断言)

**Interfaces:**
- Produces:
  - `AgentConfig` 新增字段 `MaxBlocksPerTurn int` tag `json:"max_blocks_per_turn"`
  - `Default().Agent.MaxBlocksPerTurn == 8`
  - `normalizeAgentConfig`：`MaxBlocksPerTurn<=0` 归一为 8

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/config/config_test.go`：

```go
func TestMaxBlocksPerTurnDefaultAndNormalize(t *testing.T) {
	if Default().Agent.MaxBlocksPerTurn != 8 {
		t.Fatalf("default MaxBlocksPerTurn = %d, want 8", Default().Agent.MaxBlocksPerTurn)
	}
}
```

（第 110 行的"全 0 归一化等于 Default" 测试会自动覆盖 `max_blocks_per_turn:0 → 8`，因为归一后等于 `Default().Agent`，无需改。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestMaxBlocksPerTurn -count=1`
Expected: FAIL（字段/默认未定义）。

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go` 的 `AgentConfig` 结构体，`MaxParallelBlocks` 字段后加：

```go
	MaxBlocksPerTurn          int                 `json:"max_blocks_per_turn"`
```

`defaultAgentConfig()` 里，`MaxParallelBlocks: 4,` 后加：

```go
		MaxBlocksPerTurn:          8,
```

`normalizeAgentConfig` 里，`MaxParallelBlocks` 归一化之后加：

```go
	if agent.MaxBlocksPerTurn <= 0 {
		agent.MaxBlocksPerTurn = defaults.MaxBlocksPerTurn
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/config.go tests/_packages/internal/config/config_test.go
git add internal/config/config.go tests/_packages/internal/config/config_test.go
git commit -m "feat: add max_blocks_per_turn config with default 8"
```

---

### Task 2: Runner 截断超限块 + 回灌提醒，并接入 App

**Files:**
- Modify: `internal/runtime/runner.go`
- Modify: `internal/app/engagement.go`
- Test: `tests/_packages/internal/runtime/runner_test.go` (已存在，追加测试)

**Interfaces:**
- Consumes: `AgentConfig.MaxBlocksPerTurn`（Task 1）。
- Produces:
  - `RunnerConfig` 新增 `MaxBlocksPerTurn int`
  - `Run`：提取块后若 `MaxBlocksPerTurn>0 && len(blocks) > limit`，截断为前 `limit` 个，追加 `TOO MANY BLOCKS` 用户消息 + `too_many_blocks` 恢复事件
  - `app.Service.Run` 把 `agentConfig.MaxBlocksPerTurn` 传入 `RunnerConfig`

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/runtime/runner_test.go`：

```go
func TestRunnerCapsBlocksPerTurn(t *testing.T) {
	var blocks []string
	for i := 0; i < 5; i++ {
		blocks = append(blocks, "```python\nimport os\nprint('b"+string(rune('0'+i))+"')\n```")
	}
	client := &scriptedClient{responses: []agent.Response{
		{Content: strings.Join(blocks, "\n")},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "b0\n"}}}
	config := defaultRunnerConfig()
	config.MaxBlocksPerTurn = 2
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	capped := false
	for _, event := range session.Timeline {
		if event.Kind == "recovery" && event.Detail == "too_many_blocks" {
			capped = true
		}
	}
	if !capped {
		t.Fatalf("expected too_many_blocks event: %+v", session.Timeline)
	}
	if !containsMessage(client.requests[1].Messages, "user", "TOO MANY BLOCKS") {
		t.Fatalf("cap reminder not fed back: %+v", client.requests[1].Messages)
	}
	if got := executor.lastBlockCount(); got != 2 {
		t.Fatalf("executed %d blocks, want 2", got)
	}
}
```

若 `recordingExecutor` 无 `lastBlockCount()`，在测试文件为其加一个记录最近一次 `Execute` 传入块数的最小方法（参照该文件现有 `recordingExecutor` 定义补字段与方法；若已能获知则复用）：

```go
func (e *recordingExecutor) lastBlockCount() int { return e.lastCount }
```

并在其 `Execute` 实现里记录 `e.lastCount = len(input.Blocks)`（在该文件现有实现处添加字段 `lastCount int` 与该赋值）。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run TestRunnerCapsBlocksPerTurn -count=1`
Expected: FAIL（`RunnerConfig` 无 `MaxBlocksPerTurn`，未截断，无 `too_many_blocks` 事件）。

- [ ] **Step 3: Write minimal implementation**

`internal/runtime/runner.go` 的 `RunnerConfig` 追加字段：

```go
	MaxBlocksPerTurn int
```

在 `Run` 方法内，`noCodeCount = 0` 之后、`scope := NewScope(...)` 之前插入：

```go
		if limit := runner.config.MaxBlocksPerTurn; limit > 0 && len(blocks) > limit {
			ignored := len(blocks) - limit
			session.AddEvent(turn, "recovery", "too_many_blocks", time.Now().UTC())
			history.Append("user", fmt.Sprintf("TOO MANY BLOCKS: %d code blocks were provided but only the first %d run per turn to control request rate; the remaining %d were ignored. Send fewer blocks next turn.", len(blocks), limit, ignored))
			blocks = blocks[:limit]
		}
```

（`fmt` 已在 runner.go 导入，无需新增。）

`internal/app/engagement.go` 构造 `RunnerConfig` 处，追加一行（与 `NoCodeLimit` 等并列）：

```go
		MaxBlocksPerTurn:   agentConfig.MaxBlocksPerTurn,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run TestRunnerCapsBlocksPerTurn -count=1`
再跑整包：`go test ./internal/runtime ./internal/app -count=1`
Expected: 均 PASS

- [ ] **Step 5: 全量验证**

Run:

```bash
gofmt -w internal/runtime/runner.go internal/app/engagement.go tests/_packages/internal/runtime/runner_test.go
go build ./...
go test ./...
go vet ./...
git diff --check
```

Expected: 全部通过。

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/runner.go internal/app/engagement.go tests/_packages/internal/runtime/runner_test.go
git commit -m "feat: cap code blocks per turn to control request rate"
```

---

## 自查

- **Spec 覆盖**：配置项 + 默认 8 + 归一化（Task 1）✓；Runner 截断 + 提醒 + 事件（Task 2）✓；App 接入（Task 2）✓；参照 bingo 的 8（参考依据段）✓。
- **占位符扫描**：无 TODO/TBD；`recordingExecutor.lastBlockCount` 给出实现指引且标注"若已有则复用"，非空占位。
- **类型一致性**：`AgentConfig.MaxBlocksPerTurn`（Task 1）→ `RunnerConfig.MaxBlocksPerTurn` → `app` 接入（Task 2）一致；`too_many_blocks` 事件名与 `TOO MANY BLOCKS` 提醒文本在实现与测试中一致。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接；改动测试直接改真身。
2. 截断放在 `noCodeCount = 0` 之后、`scope := NewScope(...)` 之前；`blocks` 截断后 preflight/授权/执行按现有流程处理前 N 个。
3. `recordingExecutor` 若已能获知最近执行块数则复用，勿重复定义字段。
4. 本护栏与 `max_parallel_blocks`（并发）互补，不替代它。
