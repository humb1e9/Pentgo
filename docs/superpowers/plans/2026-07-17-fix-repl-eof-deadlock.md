# 修复 REPL EOF 死锁实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 `internal/terminal` 的 REPL 丢失唤醒死锁：当输入 EOF 在一个 engagement **运行期间**到达时，`Terminal.Run` 会永久阻塞、永不返回（现象：`cmd/pentgo` 的 `TestRunREPLNaturalLanguageTaskCreatesRuntimeEngagement` 挂起，`go test ./...` 超时）。

**Root cause（已定位）:** `Terminal.Run` 的状态机中——
1. 任务行到达 → 启动 engagement，`activeDone` 置位。
2. 进入 active-select（terminal.go:113）；EOF 关闭 `lines` → `case line, ok := <-lines` 得 `ok=false` → 执行 `lines = nil; continue`（terminal.go:136-138）。**"输入已耗尽"信号在此被消费并丢弃。**
3. engagement 完成 → `activeDone` 触发 → 复位为 nil → 循环。
4. 回到 idle-select（terminal.go:92）：`lines` 已是 nil（永久阻塞），`ctx` 为 Background（永不 Done），`signals` 为空且永不关闭的 channel。**死锁。**

idle-select 仅在 `lines` **被关闭**那一刻退出（terminal.go:103-104 的 `return <-readerDone`），但此时 `lines` 已是 nil，该 case 永不触发。这是时序相关缺陷（EOF 必须落在 engagement 运行期间才触发），单元测试可确定性复现。

**Fix:** 用布尔量 `readerClosed` 记录读端是否已耗尽（替代把 `lines` 置 nil 丢失信号）。在 idle-select 之前判断：若 `readerClosed` 且无活动 engagement，则返回读端错误（`readerDone`）干净退出。active-select 里 `lines` 关闭时置 `readerClosed=true` 并停止再从 `lines` 读，但**不**丢弃退出意图。

**Tech Stack:** Go 1.25 标准库；沿用现有 `internal/terminal` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `internal/terminal/`；测试真身放 `tests/_packages/internal/terminal/`，源码目录建**相对符号链接**（`internal/terminal/<name>_test.go -> ../../tests/_packages/internal/terminal/<name>_test.go`，以现有链接形态为准）。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- 只改 REPL 退出/唤醒逻辑；不改 engagement 执行、事件记录、命令处理、prompt 输出等既有行为与测试断言。
- 保留现有 `/quit`、`/cancel`、SIGINT 取消、`ctx.Done()` 等所有既有退出路径不变。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

---

### Task 1: 修复丢失唤醒死锁（TDD）

**Files:**
- Modify: `internal/terminal/terminal.go`
- Test: `tests/_packages/internal/terminal/terminal_test.go`（已存在，追加测试）

**Interfaces:**
- Consumes: 现有 `EngagementRunner`、`readLines`。
- Produces（行为，非签名）：EOF 在 engagement 运行期间到达时，engagement 完成后 `Run` 返回 `readerDone` 的错误值（正常为 nil），不再阻塞。

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/internal/terminal/terminal_test.go`（若无以下辅助类型/函数则一并加；若已有等价物则复用，勿重复定义）：

```go
// fakeRunner 在被调用时通过 gate 控制 engagement 完成时机，模拟"EOF 先于完成到达"。
type fakeRunner struct {
	gate    chan struct{}
	started chan struct{}
}

func (r *fakeRunner) Run(ctx context.Context, _ app.Request, emit func(app.Event)) (app.Result, error) {
	close(r.started)
	select {
	case <-r.gate:
	case <-ctx.Done():
	}
	return app.Result{}, nil
}

func TestRunReturnsWhenEOFArrivesDuringEngagement(t *testing.T) {
	runner := &fakeRunner{gate: make(chan struct{}), started: make(chan struct{})}
	// 单行任务 + 立即 EOF：reader 会在 engagement 运行期间关闭 lines。
	input := strings.NewReader("对 http://example.test 做检查\n")
	term := NewWithOutputRoot(input, io.Discard, runner, make(chan os.Signal), t.TempDir())

	done := make(chan error, 1)
	go func() { done <- term.Run(context.Background()) }()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("engagement never started")
	}
	// engagement 运行中，此刻 EOF 已被 reader 送达。放行完成。
	close(runner.gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after engagement completed with EOF pending (deadlock)")
	}
}
```

（确认测试文件已 import `context`、`io`、`os`、`strings`、`time`、`pentgo/internal/app`；缺则补。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/terminal -run TestRunReturnsWhenEOFArrivesDuringEngagement -timeout 20s -count=1`
Expected: FAIL（超时——复现死锁）。

- [ ] **Step 3: Write minimal implementation**

`internal/terminal/terminal.go` 的 `Run` 方法：

新增读端耗尽标志。把（terminal.go:88-90 附近）：

```go
	var activeDone <-chan terminalRunResult
	var activeCancel context.CancelFunc
	for {
```

改为：

```go
	var activeDone <-chan terminalRunResult
	var activeCancel context.CancelFunc
	readerClosed := false
	for {
```

在 idle 分支入口（`if activeDone == nil {` 之后、`select {` 之前）加：读端已耗尽且无活动 engagement 时干净退出：

```go
		if activeDone == nil {
			if readerClosed {
				return <-readerDone
			}
			select {
```

idle-select 里 `lines` 关闭分支（terminal.go:102-105）由：

```go
			case line, ok := <-lines:
				if !ok {
					return <-readerDone
				}
```

改为置标志并让下一轮循环统一退出（保持单一退出点、语义等价）：

```go
			case line, ok := <-lines:
				if !ok {
					readerClosed = true
					lines = nil
					continue
				}
```

active-select 里 `lines` 关闭分支（terminal.go:135-139）由：

```go
			case line, ok := <-lines:
				if !ok {
					lines = nil
					continue
				}
```

改为记录读端耗尽（不再丢弃退出意图）：

```go
			case line, ok := <-lines:
				if !ok {
					readerClosed = true
					lines = nil
					continue
				}
```

（关键：`readerClosed` 一旦置位，engagement 完成回到 idle 时 `activeDone==nil` 且 `readerClosed` → `return <-readerDone`，死锁消除。`readerDone` 有缓冲、reader goroutine 结束时必写入，读取不阻塞。）

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/terminal -run TestRunReturnsWhenEOFArrivesDuringEngagement -timeout 20s -count=1`
再跑整包：`go test ./internal/terminal -timeout 30s -count=1`
Expected: 均 PASS（既有终端测试不回归）。

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/terminal/terminal.go tests/_packages/internal/terminal/terminal_test.go
git add internal/terminal/terminal.go tests/_packages/internal/terminal/terminal_test.go
git commit -m "fix: exit REPL when input EOF arrives during an engagement"
```

---

### Task 2: 回归验证（含此前挂起的 cmd/pentgo）

- [ ] **Step 1: 确认 cmd/pentgo 不再挂起**

Run: `go test ./cmd/pentgo -timeout 60s -count=1`
Expected: PASS（`TestRunREPLNaturalLanguageTaskCreatesRuntimeEngagement`、`TestRunREPLMalformedConfigWarnsAndUsesDefaults` 均通过，不再超时）。

- [ ] **Step 2: 全量回归**

```bash
go build ./...
go test ./... -timeout 120s
go vet ./...
git diff --check
```

Expected: 全部通过；**`go test ./...` 现应整体收敛不超时**（这是本计划的核心可观测收益）。

- [ ] **Step 3: Commit（若回归暴露需修项则补 commit，否则跳过）**

---

## 自查

- **Spec 覆盖**：定位并修复丢失唤醒死锁（Task 1）✓；回归含此前挂起用例（Task 2）✓。
- **占位符扫描**：无 TODO/TBD；测试与实现均为完整代码；`fakeRunner` 标注"若已有等价物则复用"。
- **类型一致性**：`readerClosed bool` 局部量；`readerDone` 复用 `readLines` 现有返回；`EngagementRunner` 接口不变，`fakeRunner` 实现其签名。
- **不回归**：`/quit`（`handleIdleLine` 返回 true）、`/cancel`、SIGINT、`ctx.Done()` 退出路径均未改动；仅新增 EOF-during-engagement 的退出与统一 idle 退出点。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接；新增测试直接改真身。
2. 确认 `readLines` 的 `done` channel 有缓冲（现为 `make(chan error, 1)`），故读端结束后 `<-readerDone` 不阻塞；若实现已变更需重新评估。
3. 本修复与 skills 迁移无关，是独立预存缺陷；不改动 engagement 执行与证据/报告产物。
4. Task 2 的 `go test ./...` 收敛是核心验收点——修复前该命令超时，修复后应整体通过。
