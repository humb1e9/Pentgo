# Skill Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让模型在系统提示词里看到可加载的 Skill 清单（名称 + 一句话描述），修复"`SKILL_LOAD` 机制存在但模型不知道有哪些 skill"的死角。

**Architecture:** `skills` 包新增 `Skill{Name, Description}` 类型与 `Catalog()`（只返回名称与描述，不含正文）。`internal/runtime` 把当前写死的 `systemPrompt` 常量改名 `baseSystemPrompt`，新增纯函数 `buildSystemPrompt(catalog []skills.Skill) string` 把清单拼进提示词末尾；`Runner` 持有可注入的 skill 目录（默认 `skills.Catalog()`），在每次运行开始时组装一次系统提示词。不改动 `SKILL_LOAD` 解析、加载上限、授权门或恢复策略。

**Tech Stack:** Go 1.25 标准库；沿用现有 `skills`、`runtime` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `internal/<pkg>/` 或 `skills/`；测试文件真身放 `tests/_packages/<same-rel-path>/<name>_test.go`，并在源码目录建**相对符号链接**（`skills/` 下测试链接目标为 `../tests/_packages/skills/<name>_test.go`；`internal/runtime/` 下为 `../../tests/_packages/internal/runtime/<name>_test.go`）。以现有 `skills/registry_test.go`、`internal/runtime/blocks_test.go` 的链接形态为准。
- 测试 package 与被测包同名（`package skills`、`package runtime`），不使用外部 `_test` 包。
- 保持 `skills.Names()` 现有行为与 `TestNamesListsRegisteredSkills`（恰好 `["recon","terminal"]`）不变。
- 保持 `skills.Load()` 现有行为与 `TestLoadReturnsRegisteredReadOnlySkill`、`TestLoadRejectsUnknownAndTraversalNames` 不变。
- Skill 描述在 registry 内维护（不解析 SKILL.md frontmatter）；描述用中文，一行、简短。
- 不改动 `SKILL_LOAD` 解析（`skillLoadPattern`）、加载上限（`maxSkillBytes`）、授权门、恢复策略及其现有测试断言。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

---

### Task 1: skills 包暴露带描述的 Catalog

**Files:**
- Modify: `skills/registry.go`
- Test: `tests/_packages/skills/registry_test.go` (已存在，追加测试)

**Interfaces:**
- Produces:
  - `type Skill struct { Name string; Description string }`
  - `func Catalog() []Skill`（按 Name 升序，不含正文）
- Consumes: 现有 `registered` map、`Names()`、`Load()`。

- [ ] **Step 1: Write the failing test**

追加到 `tests/_packages/skills/registry_test.go`：

```go
func TestCatalogListsSkillsWithDescriptions(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 2 {
		t.Fatalf("catalog length = %d", len(catalog))
	}
	if catalog[0].Name != "recon" || catalog[1].Name != "terminal" {
		t.Fatalf("catalog order = %+v", catalog)
	}
	for _, skill := range catalog {
		if strings.TrimSpace(skill.Description) == "" {
			t.Fatalf("skill %q has empty description", skill.Name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./skills -run TestCatalog -count=1`
Expected: FAIL（编译错误：`Skill`、`Catalog` 未定义）。

- [ ] **Step 3: Write minimal implementation**

在 `skills/registry.go` 里，把当前的：

```go
var registered = map[string]string{
	"recon":    reconPrompt,
	"terminal": terminalPrompt,
}
```

替换为携带描述的结构，并新增类型与 `Catalog`（`Load`/`Names` 改为读取 `.content`/键，行为不变）：

```go
type skillEntry struct {
	description string
	content     string
}

var registered = map[string]skillEntry{
	"recon": {
		description: "信息收集方法论：以已回灌的执行输出为依据，逐步减少目标未知信息。",
		content:     reconPrompt,
	},
	"terminal": {
		description: "终端 Agent 通用准则：只把已执行并回灌的输出当作证据。",
		content:     terminalPrompt,
	},
}

// Skill 是可加载 Skill 的目录条目，不含正文。
type Skill struct {
	Name        string
	Description string
}

// Catalog 返回按名称升序排列的可加载 Skill 目录（名称与描述，不含正文）。
func Catalog() []Skill {
	catalog := make([]Skill, 0, len(registered))
	for name, entry := range registered {
		catalog = append(catalog, Skill{Name: name, Description: entry.description})
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	return catalog
}
```

同步把 `Names()` 与 `Load()` 内对 `registered` 的读取改为新结构：

`Names()` 内循环体不变（仍 `for name := range registered`）。`Load()` 改为：

```go
// Load 返回一个注册的只读 Skill，内容被限制为模型上下文上限。
func Load(name string) (string, error) {
	entry, ok := registered[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	if len(entry.content) > maxSkillBytes {
		return entry.content[:maxSkillBytes], nil
	}
	return entry.content, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./skills -count=1`
Expected: PASS（新 `TestCatalog` 通过，且 `TestLoad*`、`TestNames*` 全部保持通过）。

- [ ] **Step 5: Commit**

```bash
gofmt -w skills/registry.go tests/_packages/skills/registry_test.go
git add skills/registry.go tests/_packages/skills/registry_test.go
git commit -m "feat: expose skill catalog with descriptions"
```

---

### Task 2: 把 Skill 清单注入系统提示词

**Files:**
- Modify: `internal/runtime/prompt.go`
- Modify: `internal/runtime/runner.go`
- Test: `tests/_packages/internal/runtime/prompt_test.go` (新增)
- Create symlink: `internal/runtime/prompt_test.go -> ../../tests/_packages/internal/runtime/prompt_test.go`
- Test: `tests/_packages/internal/runtime/runner_test.go` (已存在，追加测试)

**Interfaces:**
- Consumes: `skills.Skill`、`skills.Catalog`（Task 1）。
- Produces:
  - `internal/runtime/prompt.go`：常量改名 `baseSystemPrompt`；新增 `func buildSystemPrompt(catalog []skills.Skill) string`
  - `RunnerConfig` 新增字段 `SkillCatalog []skills.Skill`
  - `Runner` 新增字段 `catalog []skills.Skill`；`NewRunner` 中 nil 时默认 `skills.Catalog()`
  - `Runner.Run` 用 `buildSystemPrompt(runner.catalog)` 替换直接引用的 `systemPrompt`

- [ ] **Step 1: Write the failing test**

创建 `tests/_packages/internal/runtime/prompt_test.go`：

```go
package runtime

import (
	"strings"
	"testing"

	"pentgo/skills"
)

func TestBuildSystemPromptListsSkills(t *testing.T) {
	prompt := buildSystemPrompt([]skills.Skill{
		{Name: "recon", Description: "信息收集方法论"},
		{Name: "terminal", Description: "终端通用准则"},
	})
	for _, want := range []string{"SKILL_LOAD", "recon", "信息收集方法论", "terminal", "终端通用准则"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestBuildSystemPromptWithoutSkills(t *testing.T) {
	prompt := buildSystemPrompt(nil)
	if !strings.Contains(prompt, "terminal agent") {
		t.Fatalf("base prompt missing: %s", prompt)
	}
}
```

创建符号链接：

```bash
ln -s ../../tests/_packages/internal/runtime/prompt_test.go internal/runtime/prompt_test.go
```

追加到 `tests/_packages/internal/runtime/runner_test.go`（断言发给模型的系统提示词含注入的清单）：

```go
func TestRunnerInjectsSkillCatalogIntoSystemPrompt(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "TASK_COMPLETE"}}}
	config := defaultRunnerConfig()
	config.SkillCatalog = []skills.Skill{{Name: "recon", Description: "信息收集方法论"}}
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) == 0 {
		t.Fatal("no requests recorded")
	}
	prompt := client.requests[0].SystemPrompt
	if !strings.Contains(prompt, "recon") || !strings.Contains(prompt, "信息收集方法论") {
		t.Fatalf("system prompt missing catalog: %s", prompt)
	}
}
```

在 `runner_test.go` 的 import 块加入 `"pentgo/skills"`（若尚未导入）。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestBuildSystemPrompt|TestRunnerInjectsSkillCatalog' -count=1`
Expected: FAIL（`buildSystemPrompt` 未定义，`RunnerConfig` 无 `SkillCatalog` 字段）。

- [ ] **Step 3: Write minimal implementation**

把 `internal/runtime/prompt.go` 整体替换为（常量改名 + 新增组装函数）：

```go
package runtime

import (
	"strings"

	"pentgo/skills"
)

const baseSystemPrompt = `You are PentGo's terminal agent. Work from actual execution output only.

When an operation is needed, return one or more fenced Python or Bash code blocks. Every block must print useful evidence. The runtime executes all supported blocks and returns stdout, stderr, exit status, and evidence references in the next turn.

Use SKILL_LOAD: skill-name on its own line to request a registered local skill. Skills are read-only context, not native tools. Return TASK_COMPLETE or MISSION_COMPLETE only after the required evidence has been returned and do not include code in a completion response.`

// buildSystemPrompt 在基础提示词后追加可加载 Skill 清单，供模型发现并按需 SKILL_LOAD。
func buildSystemPrompt(catalog []skills.Skill) string {
	if len(catalog) == 0 {
		return baseSystemPrompt
	}
	var builder strings.Builder
	builder.WriteString(baseSystemPrompt)
	builder.WriteString("\n\nAvailable skills (load with SKILL_LOAD: <name> when relevant):\n")
	for _, skill := range catalog {
		builder.WriteString("- ")
		builder.WriteString(skill.Name)
		builder.WriteString(": ")
		builder.WriteString(skill.Description)
		builder.WriteString("\n")
	}
	return builder.String()
}
```

在 `internal/runtime/runner.go` 的 `RunnerConfig` 结构体追加字段（`OnEvent` 之后、或与 Task 4 授权字段并列）：

```go
	SkillCatalog []skills.Skill
```

在 `Runner` 结构体追加字段（`reportTurns` 之后）：

```go
	catalog []skills.Skill
```

在 `NewRunner` 内，返回 `&Runner{...}` 之前解析默认目录：

```go
	catalog := config.SkillCatalog
	if catalog == nil {
		catalog = skills.Catalog()
	}
```

并把返回语句改为带上 `catalog`：

```go
	return &Runner{client: client, executor: executor, config: config, load: load, sleep: sleep, catalog: catalog}
```

在 `Run` 方法内，把当前的：

```go
		response, err := runner.chat(ctx, agent.Request{SystemPrompt: systemPrompt, Messages: history.Messages()})
```

替换为使用组装后的提示词（在循环外算一次）。在 `Run` 里 `history := NewHistory(...)` 之后新增：

```go
	systemPrompt := buildSystemPrompt(runner.catalog)
```

循环内 `runner.chat` 调用保持引用局部变量 `systemPrompt`（现在是 `buildSystemPrompt` 的结果，而非已删除的同名常量）。

说明：`runner.go` 已 import `"pentgo/skills"`（用于 `skills.Load` 默认加载器），无需新增 import。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run 'TestBuildSystemPrompt|TestRunnerInjectsSkillCatalog' -count=1`
Expected: PASS

再跑整包回归：

Run: `go test ./internal/runtime -count=1`
Expected: PASS（现有 runner/prompt 相关测试不受影响）。

- [ ] **Step 5: 全量验证**

Run:

```bash
gofmt -w internal/runtime/prompt.go internal/runtime/runner.go tests/_packages/internal/runtime/prompt_test.go tests/_packages/internal/runtime/runner_test.go
go build ./...
go test ./...
go vet ./...
git diff --check
```

Expected: 全部通过；`go build`/`go vet` 无输出即成功。

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/prompt.go internal/runtime/runner.go tests/_packages/internal/runtime/prompt_test.go internal/runtime/prompt_test.go tests/_packages/internal/runtime/runner_test.go
git commit -m "feat: inject skill catalog into system prompt"
```

---

## 自查

- **Spec 覆盖**：模型可见 skill 清单（Task 2 `buildSystemPrompt` 注入名称+描述）✓；清单来源含描述（Task 1 `Catalog`）✓；默认取真实注册表、测试可注入假清单（Task 2 `NewRunner` 默认 + `SkillCatalog` 字段）✓；无 skill 时回退基础提示词（Task 2 `TestBuildSystemPromptWithoutSkills`）✓。
- **占位符扫描**：无 TODO/TBD；每个代码步骤含完整代码与预期输出。
- **类型一致性**：`Skill{Name,Description}`/`Catalog`（Task 1）→ `buildSystemPrompt([]skills.Skill)`、`RunnerConfig.SkillCatalog`、`Runner.catalog`、`NewRunner` 默认（Task 2）全部一致；常量 `baseSystemPrompt` 改名后 `Run` 内改用局部变量 `systemPrompt := buildSystemPrompt(...)`，无悬空引用。

## 集成前提（执行者须先确认）

1. `skills/registry_test.go` 与 `internal/runtime/*_test.go` 均为指向 `tests/_packages/` 的相对符号链接；新增测试先建真身再建链接。
2. Task 1 改 `registered` 为结构体 map 后，务必同步更新 `Load()` 与 `Names()` 的读取方式，保持三个既有测试通过。
3. 若 `runner_test.go` 已 import `pentgo/skills` 则不要重复导入。
4. 本计划不改动 `SKILL_LOAD` 解析、加载上限、授权门与恢复策略。
