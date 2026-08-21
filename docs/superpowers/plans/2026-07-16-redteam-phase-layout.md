# Red Team 阶段目录迁移 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有 Recon 和 HTTP 元数据采集器迁移到 Red Team 阶段目录，并预留后续阶段、工具与通用辅助边界。

**Architecture:** `internal/redteam` 继续持有会话、授权与阶段编排；现有 `recon` 包迁移为 `internal/redteam/phases/recon`，其唯一使用者 `httpmeta` 随之成为 `recon/httpmeta` 子包。尚未实现的阶段只保留说明文件，避免新增空 Go 包和运行时行为。

**Tech Stack:** Go 1.25 标准工具链、Git、现有测试镜像符号链接布局。

## Global Constraints

- Go 版本保持 `1.25.0`，不新增第三方依赖。
- 保持 REPL、Recon 配置、证据和报告行为不变。
- 所有新增说明文件使用中文；测试物理文件继续位于 `tests/`。
- 不修改 `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md`，它是历史计划且存在用户未提交修改。
- `internal/utils` 只接收被两个或更多包复用的无状态基础辅助函数；网络 I/O、命令执行、阶段状态和模型协议不进入该目录。

---

### Task 1: Establish the Relocation Safety Net

**Files:**
- Verify: `internal/recon/*.go`
- Verify: `internal/httpmeta/collector.go`
- Verify: `tests/packages/internal/recon/*_test.go`
- Verify: `tests/_packages/internal/httpmeta/collector_test.go`

**Interfaces:**
- Consumes: 当前 `pentgo/internal/recon` 和 `pentgo/internal/httpmeta` 包。
- Produces: 重构前的测试基线；不改动生产行为。

- [x] **Step 1: Run the current full test suite**

Run: `go test ./... -count=1`

Expected: 所有现有包测试通过，为纯目录迁移建立基线。

- [x] **Step 2: Run the focused package tests**

Run: `go test ./tests/packages/internal/recon ./internal/httpmeta ./internal/redteam ./internal/app -count=1`

Expected: Recon、HTTP 元数据、Pipeline 和应用服务的现有测试通过。

### Task 2: Move Recon and HTTP Metadata into the Phase Tree

**Files:**
- Move: `internal/recon/` to `internal/redteam/phases/recon/`
- Move: `internal/httpmeta/` to `internal/redteam/phases/recon/httpmeta/`
- Move: `tests/packages/internal/recon/` to `tests/packages/internal/redteam/phases/recon/`
- Move: `tests/_packages/internal/httpmeta/` to `tests/_packages/internal/redteam/phases/recon/httpmeta/`
- Modify: `internal/app/engagement.go`
- Modify: `internal/redteam/recon_pipeline.go`
- Modify: `internal/redteam/session.go`
- Modify: `internal/redteam/verification.go`
- Modify: `tests/_packages/cmd/pentgo/e2e_test.go`
- Modify: `tests/_packages/internal/redteam/recon_pipeline_test.go`
- Modify: `tests/_packages/internal/redteam/session_state_test.go`
- Modify: `tests/_packages/internal/redteam/verification_test.go`
- Modify: `tests/_packages/internal/report/artifacts_test.go`
- Modify: `tests/packages/internal/redteam/phases/recon/native_test.go`

**Interfaces:**
- Consumes: `recon.Runner`, `recon.State`, `httpmeta.Collector` 和现有测试镜像布局。
- Produces: `pentgo/internal/redteam/phases/recon` 与 `pentgo/internal/redteam/phases/recon/httpmeta`，包名分别保持 `recon` 和 `httpmeta`。

- [x] **Step 1: Move source and physical test directories with Git**

Run:

```bash
mkdir -p internal/redteam/phases
git mv internal/recon internal/redteam/phases/recon
git mv internal/httpmeta internal/redteam/phases/recon/httpmeta
mkdir -p tests/packages/internal/redteam/phases
git mv tests/packages/internal/recon tests/packages/internal/redteam/phases/recon
mkdir -p tests/_packages/internal/redteam/phases/recon
git mv tests/_packages/internal/httpmeta tests/_packages/internal/redteam/phases/recon/httpmeta
```

Expected: Git 将源文件和测试源识别为重命名；包声明继续为 `recon` 与 `httpmeta`。

- [x] **Step 2: Update production and external-package imports**

Replace each production and test import of:

```go
"pentgo/internal/recon"
```

with:

```go
"pentgo/internal/redteam/phases/recon"
```

Replace the import in `native_test.go` of:

```go
"pentgo/internal/httpmeta"
```

with:

```go
"pentgo/internal/redteam/phases/recon/httpmeta"
```

Keep each local package identifier as `recon` or `httpmeta`; no function signatures or runtime behavior change.

- [x] **Step 3: Repair source-test mirror symlinks**

Update the moved Recon source links under `tests/packages/internal/redteam/phases/recon/` to these exact targets:

```text
agent_catalog.go -> ../../../../../../internal/redteam/phases/recon/agent_catalog.go
agent_runner.go -> ../../../../../../internal/redteam/phases/recon/agent_runner.go
bingo_skills.go -> ../../../../../../internal/redteam/phases/recon/bingo_skills.go
fofa.go -> ../../../../../../internal/redteam/phases/recon/fofa.go
native.go -> ../../../../../../internal/redteam/phases/recon/native.go
native_http.go -> ../../../../../../internal/redteam/phases/recon/native_http.go
passive.go -> ../../../../../../internal/redteam/phases/recon/passive.go
runner.go -> ../../../../../../internal/redteam/phases/recon/runner.go
shodan.go -> ../../../../../../internal/redteam/phases/recon/shodan.go
skill_runner.go -> ../../../../../../internal/redteam/phases/recon/skill_runner.go
state.go -> ../../../../../../internal/redteam/phases/recon/state.go
types.go -> ../../../../../../internal/redteam/phases/recon/types.go
```

For `internal/redteam/phases/recon/httpmeta/collector_test.go`, point to:

```text
../../../../../tests/_packages/internal/redteam/phases/recon/httpmeta/collector_test.go
```

The source links must remain symbolic links, preserving the repository's test-source layout.

- [x] **Step 4: Format and run the relocated focused tests**

Run:

```bash
gofmt -w $(rg --files internal/app internal/redteam internal/redteam/phases tests/_packages tests/packages -g '*.go')
go test ./tests/packages/internal/redteam/phases/recon ./internal/redteam/phases/recon/httpmeta ./internal/redteam ./internal/app -count=1
```

Expected: The moved packages compile and all focused tests pass under their new import paths.

### Task 3: Reserve Future Stage, Tool, and Utility Boundaries

**Files:**
- Create: `internal/redteam/phases/scan/README.md`
- Create: `internal/redteam/phases/exploit/README.md`
- Create: `internal/redteam/phases/webshell/README.md`
- Create: `internal/redteam/phases/idor/README.md`
- Create: `internal/tools/README.md`
- Create: `internal/utils/README.md`
- Remove: empty `internal/llm/`
- Remove: empty `internal/orchestrator/`

**Interfaces:**
- Consumes: 已确认的阶段、工具与辅助边界。
- Produces: Git 可跟踪的预留目录，且不创建空 Go 包。

- [x] **Step 1: Add the future-stage directory descriptions**

Create `internal/redteam/phases/scan/README.md`:

```markdown
# Scan 阶段

此目录预留给漏洞扫描阶段的编排与阶段专属实现。

仅放入 Scan 专属代码；跨阶段工具适配器放入 `internal/tools`，无状态通用辅助函数放入 `internal/utils`。
```

Create `internal/redteam/phases/exploit/README.md`:

```markdown
# Exploit 阶段

此目录预留给已验证发现的利用验证阶段实现。

仅放入 Exploit 专属代码；跨阶段工具适配器放入 `internal/tools`，无状态通用辅助函数放入 `internal/utils`。
```

Create `internal/redteam/phases/webshell/README.md`:

```markdown
# Webshell 阶段

此目录预留给 Webshell 阶段的编排与阶段专属实现。

仅放入 Webshell 专属代码；跨阶段工具适配器放入 `internal/tools`，无状态通用辅助函数放入 `internal/utils`。
```

Create `internal/redteam/phases/idor/README.md`:

```markdown
# IDOR 阶段

此目录预留给 IDOR 与授权验证阶段的编排与阶段专属实现。

仅放入 IDOR 专属代码；跨阶段工具适配器放入 `internal/tools`，无状态通用辅助函数放入 `internal/utils`。
```

- [x] **Step 2: Add tool and utility boundary descriptions**

Create `internal/tools/README.md`:

```markdown
# 跨阶段工具

此目录用于被两个或更多 Red Team 阶段复用的具体工具适配器。

阶段专属实现留在相应的 `internal/redteam/phases/` 子目录；allowlisted 本地命令执行继续由 `internal/skill` 管理。
```

Create `internal/utils/README.md`:

```markdown
# 通用辅助函数

此目录只放入被两个或更多包复用的无状态基础辅助函数。

网络 I/O、命令执行、配置读取、模型协议、阶段编排和领域状态均不放入此目录。
```

- [x] **Step 3: Remove stale empty directories and inspect the target tree**

Run:

```bash
rmdir internal/llm internal/orchestrator
find internal/redteam/phases -maxdepth 2 -type d | sort
find internal -maxdepth 1 -type d | sort
```

Expected: 仅保留已定义职责的内部目录；Scan、Exploit、Webshell、IDOR、tools 与 utils 都由说明文件跟踪。

### Task 4: Update Current Architecture Documentation and Verify the Refactor

**Files:**
- Modify: `docs/superpowers/specs/2026-07-16-recon-agent-loop-design.md`
- Verify: `docs/superpowers/specs/2026-07-16-redteam-phase-layout-design.md`
- Verify: all Go packages and test mirrors

**Interfaces:**
- Consumes: 已迁移的包路径和预留目录。
- Produces: 当前架构文档与源码路径一致；历史计划保持不变。

- [x] **Step 1: Update the current Recon design path references**

Change the current architecture heading and path references from `internal/recon` to `internal/redteam/phases/recon`. Keep historical paths in `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md` unchanged.

- [x] **Step 2: Check imports, links, and formatting before the full suite**

Run:

```bash
if rg -n 'pentgo/internal/(recon|httpmeta)' -g '*.go' .; then
  exit 1
fi
find tests/packages/internal/redteam/phases/recon -maxdepth 1 -type l -printf '%p -> %l\\n' | sort
git diff --check
```

Expected: The import search emits no matches; every Recon mirror is a valid source link to `internal/redteam/phases/recon`.

- [x] **Step 3: Run the complete verification suite**

Run:

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
go mod verify
git diff --check
```

Expected: Every command exits with status 0.

- [x] **Step 4: Review the final change set and commit**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Stage only the relocated source, relocated tests, new directory documentation, and current architecture documentation. Do not stage `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md`. Commit with:

```bash
git commit -m "refactor: organize red team phase packages"
```
