# Post-Verification Refinements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 依据真实 engagement 实测结论修三处：(1) `max_turns=0` 即不限轮次，由模型自行决定何时 `TASK_COMPLETE`；(2) 证据分级改为"模型自声明的发现标签"，与现有"执行成功度"分开记录；(3) 迁移若干冷门高价值 Skill，让 `SKILL_LOAD` 真正有值得加载的内容。

**Architecture:** `max_turns` 语义改为 `<=0` 表示不限，失控防护退回到既有的软/硬 stuck 指纹检测与 no-code 上限。发现标签从模型回复文本中提取 `[VERIFIED]/[LIKELY]/[INFERRED]`，记在 `ReportTurn` 上（回合级，与代码块级的执行 `Level` 分离），报告模型据此区分"模型声明的发现"与"仅执行成功"。冷门 Skill 从 `bingo/skills` 迁入 `skills/`，作为 `//go:embed` 只读内容 + registry 描述条目，`SKILL_LOAD` 机制与提示词清单（已实现）无需改动。

**Tech Stack:** Go 1.25 标准库；沿用现有 `runtime`、`config`、`skills`、`report` 包与 TDD/符号链接测试布局。

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `internal/<pkg>/` 或 `skills/`；测试真身放 `tests/_packages/<same-rel-path>/`，源码目录建**相对符号链接**（`internal/runtime/` 下 `../../tests/_packages/internal/runtime/`；`skills/` 下 `../tests/_packages/skills/`），以现有链接形态为准。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- 发现标签词汇沿用 `VERIFIED/LIKELY/INFERRED`，与提示词及 `EvidenceLevel` 常量一致；不引入 `AI_ANALYSIS`。
- **不**引入"运行时扫描输出判定漏洞"的启发式检测——发现标签只来自模型自声明文本，执行 `Level` 只来自 `GradeEvidence`（执行成功度），二者分离。
- 去掉轮次硬顶后，失控防护依赖既有 `hard_stuck_turns`/`soft_stuck_turns`/`no_code_limit`/取消；本计划不削弱它们。
- 迁移的 Skill 为只读 Markdown；`SKILL_LOAD` 解析、授权门、执行器进程隔离不改动。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

---

### Task 1: max_turns=0 表示不限轮次

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/runtime/runner.go`
- Test: `tests/_packages/internal/config/config_test.go` (已存在，改断言)
- Test: `tests/_packages/internal/runtime/runner_test.go` (已存在，追加测试)

**Interfaces:**
- Produces:
  - `Default().Agent.MaxTurns == 0`（不限）
  - `normalizeAgentConfig` 不再把 `MaxTurns<=0` 改写为 20
  - `NewRunner` 不再把 `MaxTurns<=0` 改写为 20
  - `Runner.Run` 循环条件：`MaxTurns<=0` 时不设轮次上限

- [ ] **Step 1: Write the failing test**

改 `tests/_packages/internal/config/config_test.go` 第 15 行附近的默认断言，把 `agent.MaxTurns != 20` 改为 `agent.MaxTurns != 0`：

```go
	if agent.Provider != "openai" || agent.MaxTurns != 0 || agent.RequestTimeoutSeconds != 60 {
```

（第 110 行的归一化测试 `max_turns:0` 期望等于 `Default().Agent`——默认改为 0 后仍相等，无需改。）

追加到 `tests/_packages/internal/runtime/runner_test.go`：

```go
func TestRunnerRunsUnboundedUntilCompletionWhenMaxTurnsZero(t *testing.T) {
	responses := make([]agent.Response, 0, 26)
	for i := 0; i < 25; i++ {
		responses = append(responses, agent.Response{Content: "```python\nimport os\nprint('probe')\n```"})
	}
	responses = append(responses, agent.Response{Content: "TASK_COMPLETE"})
	client := &scriptedClient{responses: responses}
	executor := &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "probe\n"}}}
	config := defaultRunnerConfig()
	config.MaxTurns = 0
	config.SoftStuckTurns = 1000
	config.HardStuckTurns = 1000
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone || session.Turn != 26 {
		t.Fatalf("session = %+v", session)
	}
}
```

（每轮代码内容需各不相同以免触发 stuck；上例每轮相同，故把 stuck 阈值调到 1000 规避——真实运行由 stuck 指纹兜底。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config ./internal/runtime -run 'TestDefault|TestRunnerRunsUnbounded' -count=1`
Expected: FAIL（默认仍是 20；`MaxTurns<=0` 被改写为 20，循环在第 20 轮 fail）。

- [ ] **Step 3: Write minimal implementation**

`internal/config/config.go` 的 `defaultAgentConfig()` 把 `MaxTurns: 20,` 改为：

```go
		MaxTurns:                  0,
```

`normalizeAgentConfig` 删除这三行（不再强制默认）：

```go
	if agent.MaxTurns <= 0 {
		agent.MaxTurns = defaults.MaxTurns
	}
```

`internal/runtime/runner.go` 的 `NewRunner` 删除：

```go
	if config.MaxTurns <= 0 {
		config.MaxTurns = 20
	}
```

`Run` 方法把循环头：

```go
	for session.Turn < runner.config.MaxTurns {
```

改为：

```go
	for runner.config.MaxTurns <= 0 || session.Turn < runner.config.MaxTurns {
```

循环末尾的 `_ = session.Fail("max_turns", ...)` 保留——仅当 `MaxTurns>0` 且真的跑满时才会到达。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config ./internal/runtime -run 'TestDefault|TestRunnerRunsUnbounded' -count=1`
再跑整包：`go test ./internal/config ./internal/runtime -count=1`
Expected: 均 PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/config.go internal/runtime/runner.go tests/_packages/internal/config/config_test.go tests/_packages/internal/runtime/runner_test.go
git add internal/config/config.go internal/runtime/runner.go tests/_packages/internal/config/config_test.go tests/_packages/internal/runtime/runner_test.go
git commit -m "feat: treat max_turns<=0 as unbounded, model decides completion"
```

---

### Task 2: 模型自声明的发现标签（与执行 Level 分离）

**Files:**
- Create: `internal/runtime/finding_label.go`
- Modify: `internal/runtime/report_context.go`
- Modify: `internal/runtime/runner.go`
- Modify: `internal/report/generator.go`
- Create: `tests/_packages/internal/runtime/finding_label_test.go`
- Create symlink: `internal/runtime/finding_label_test.go -> ../../tests/_packages/internal/runtime/finding_label_test.go`
- Test: `tests/_packages/internal/runtime/report_context_test.go` (已存在，追加测试)

**Interfaces:**
- Consumes: `EvidenceLevel`、`EvidenceVerified/Likely/Inferred`（现有 `evidence_grade.go`）。
- Produces:
  - `func extractFindingLabels(text string) []EvidenceLevel`（从模型文本提取 `[VERIFIED]/[LIKELY]/[INFERRED]`，按出现顺序去重）
  - `ReportTurn` 新增字段 `DeclaredLabels []EvidenceLevel`
  - runner 记录回合时填入 `extractFindingLabels(assistantText)`
  - `ReportContext.PromptText` 输出该回合的"模型声明标签"，与代码块的执行 `Level` 分列
  - 报告系统提示词说明：发现强度以模型声明标签为准，执行 `Level` 仅为技术旁证

**说明：** 代码块的 `ExecutionResult.Level`（执行成功度，`GradeEvidence`）保持不变；本 Task 只新增回合级的模型声明标签，二者分离，互不覆盖。

- [ ] **Step 1: Write the failing test**

创建 `tests/_packages/internal/runtime/finding_label_test.go`：

```go
package runtime

import (
	"reflect"
	"testing"
)

func TestExtractFindingLabels(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []EvidenceLevel
	}{
		{"single likely", "[LIKELY] found an open API", []EvidenceLevel{EvidenceLikely}},
		{"mixed order dedup", "[VERIFIED] a\n[INFERRED] b\n[VERIFIED] c", []EvidenceLevel{EvidenceVerified, EvidenceInferred}},
		{"none", "just reconnaissance, no findings", nil},
		{"all three", "[VERIFIED] x [LIKELY] y [INFERRED] z", []EvidenceLevel{EvidenceVerified, EvidenceLikely, EvidenceInferred}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractFindingLabels(test.text); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("extractFindingLabels(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}
```

创建符号链接：

```bash
ln -s ../../tests/_packages/internal/runtime/finding_label_test.go internal/runtime/finding_label_test.go
```

追加到 `tests/_packages/internal/runtime/report_context_test.go`：

```go
func TestReportContextRendersDeclaredLabels(t *testing.T) {
	context := ReportContext{
		Target: "https://example.com",
		Turns: []ReportTurn{{
			Number:         1,
			Decision:       "发现未授权接口",
			DeclaredLabels: []EvidenceLevel{EvidenceLikely},
		}},
	}
	text := context.PromptText()
	if !strings.Contains(text, "LIKELY") {
		t.Fatalf("prompt missing declared label: %s", text)
	}
}
```

（若该测试文件尚未 import `"strings"`，补上。）

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime -run 'TestExtractFindingLabels|TestReportContextRendersDeclaredLabels' -count=1`
Expected: FAIL（`extractFindingLabels` 未定义、`ReportTurn` 无 `DeclaredLabels`）。

- [ ] **Step 3: Write minimal implementation**

创建 `internal/runtime/finding_label.go`：

```go
package runtime

import "regexp"

var findingLabelPattern = regexp.MustCompile(`\[(VERIFIED|LIKELY|INFERRED)\]`)

// extractFindingLabels 从模型回复文本按出现顺序提取去重后的发现标签。
func extractFindingLabels(text string) []EvidenceLevel {
	matches := findingLabelPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[EvidenceLevel]bool, 3)
	labels := make([]EvidenceLevel, 0, len(matches))
	for _, match := range matches {
		level := EvidenceLevel(match[1])
		if seen[level] {
			continue
		}
		seen[level] = true
		labels = append(labels, level)
	}
	return labels
}
```

`internal/runtime/report_context.go` 的 `ReportTurn` 结构体，`Decision` 字段后加：

```go
	DeclaredLabels []EvidenceLevel
```

在 `PromptText` 里，渲染回合决策后追加声明标签行。把当前：

```go
		if !appendReportText(&builder, fmt.Sprintf("\n回合 %d\n决策摘要: %s\n", turn.Number, turn.Decision)) {
			return finishReportPrompt(&builder)
		}
```

改为其后补一段（保持有界写入约定）：

```go
		if !appendReportText(&builder, fmt.Sprintf("\n回合 %d\n决策摘要: %s\n", turn.Number, turn.Decision)) {
			return finishReportPrompt(&builder)
		}
		if len(turn.DeclaredLabels) > 0 {
			labels := make([]string, 0, len(turn.DeclaredLabels))
			for _, label := range turn.DeclaredLabels {
				labels = append(labels, string(label))
			}
			if !appendReportText(&builder, "模型声明标签: "+strings.Join(labels, ", ")+"\n") {
				return finishReportPrompt(&builder)
			}
		}
```

`internal/runtime/runner.go` 在追加 `ReportTurn` 处（`runner.reportTurns = append(...)`）把标签填进去。当前：

```go
		runner.reportTurns = append(runner.reportTurns, ReportTurn{Number: turn, Decision: assistantSummary(assistantText)})
```

改为：

```go
		runner.reportTurns = append(runner.reportTurns, ReportTurn{Number: turn, Decision: assistantSummary(assistantText), DeclaredLabels: extractFindingLabels(assistantText)})
```

`internal/report/generator.go` 的 `terminalReportSystemPrompt`，在证据约束段后补一句（说明标签口径）：

```
发现强度以“模型声明标签”（VERIFIED/LIKELY/INFERRED）为准：只有 VERIFIED、LIKELY 且有对应执行证据的项列入“已验证发现”；INFERRED 或无执行证据支撑的项归入“未完成或受阻项目”。代码块的执行状态仅为技术旁证，不等于漏洞被证实。
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/runtime -run 'TestExtractFindingLabels|TestReportContextRendersDeclaredLabels' -count=1`
再跑整包：`go test ./internal/runtime ./internal/report -count=1`
Expected: 均 PASS

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/finding_label.go internal/runtime/report_context.go internal/runtime/runner.go internal/report/generator.go tests/_packages/internal/runtime/finding_label_test.go tests/_packages/internal/runtime/report_context_test.go
git add internal/runtime/finding_label.go internal/runtime/report_context.go internal/runtime/runner.go internal/report/generator.go tests/_packages/internal/runtime/finding_label_test.go internal/runtime/finding_label_test.go tests/_packages/internal/runtime/report_context_test.go
git commit -m "feat: capture model-declared finding labels separate from execution level"
```

---

### Task 3: 迁移冷门高价值 Skill

**Files:**
- Create: `skills/waf-bypass/SKILL.md`（从 `bingo/skills/hack-skills/waf-bypass-techniques/SKILL.md` 复制）
- Create: `skills/nosql-injection/SKILL.md`（从 `bingo/skills/hack-skills/nosql-injection/SKILL.md` 复制）
- Create: `skills/type-juggling/SKILL.md`（从 `bingo/skills/hack-skills/type-juggling/SKILL.md` 复制）
- Modify: `skills/registry.go`
- Test: `tests/_packages/skills/registry_test.go` (已存在，改断言)

**Interfaces:**
- Produces:
  - `maxSkillBytes` 提升到 16000（容纳迁入 Skill；仍受报告/历史其它上限约束）
  - registry 新增 `waf-bypass`、`nosql-injection`、`type-juggling` 三个条目（名称匹配 `SKILL_LOAD` 的 `[a-z][a-z0-9_-]*`）
  - `Names()` 返回 5 个、`Catalog()` 返回 5 个（升序）

**说明：** 只迁移只读 Markdown 知识，不迁移 bingo 的 Python 工具实现。ghost-bits-cast-attack（30KB）价值最高但超限，需裁剪后单独加，本 Task 不含。

- [ ] **Step 1: Write the failing test**

改 `tests/_packages/skills/registry_test.go` 的 `TestNamesListsRegisteredSkills`：

```go
func TestNamesListsRegisteredSkills(t *testing.T) {
	names := Names()
	want := []string{"nosql-injection", "recon", "terminal", "type-juggling", "waf-bypass"}
	if len(names) != len(want) {
		t.Fatalf("names = %q", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %q, want %q", names, want)
		}
	}
}
```

改 `TestCatalogListsSkillsWithDescriptions` 的长度断言 `if len(catalog) != 2` 为 `if len(catalog) != 5`，并把 `catalog[0].Name != "recon" || catalog[1].Name != "terminal"` 改为验证首个为 `nosql-injection`（升序后）：

```go
	if len(catalog) != 5 {
		t.Fatalf("catalog length = %d", len(catalog))
	}
	if catalog[0].Name != "nosql-injection" {
		t.Fatalf("catalog order = %+v", catalog)
	}
```

新增一个加载迁入 Skill 的测试：

```go
func TestLoadMigratedSkill(t *testing.T) {
	for _, name := range []string{"waf-bypass", "nosql-injection", "type-juggling"} {
		content, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q) error = %v", name, err)
		}
		if strings.TrimSpace(content) == "" {
			t.Fatalf("Load(%q) returned empty", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./skills -count=1`
Expected: FAIL（名称/目录仍为 2 个；三个新 Skill 未注册；`embed` 目标文件不存在→编译失败）。

- [ ] **Step 3: Write minimal implementation**

先复制 Skill 内容文件：

```bash
mkdir -p skills/waf-bypass skills/nosql-injection skills/type-juggling
cp bingo/skills/hack-skills/waf-bypass-techniques/SKILL.md skills/waf-bypass/SKILL.md
cp bingo/skills/hack-skills/nosql-injection/SKILL.md skills/nosql-injection/SKILL.md
cp bingo/skills/hack-skills/type-juggling/SKILL.md skills/type-juggling/SKILL.md
```

`skills/registry.go` 把 `maxSkillBytes` 改为：

```go
const maxSkillBytes = 16000
```

在现有 `//go:embed terminal/SKILL.md` 之后追加三个 embed 变量：

```go
//go:embed waf-bypass/SKILL.md
var wafBypassPrompt string

//go:embed nosql-injection/SKILL.md
var nosqlInjectionPrompt string

//go:embed type-juggling/SKILL.md
var typeJugglingPrompt string
```

在 `registered` map 里追加三个条目：

```go
	"waf-bypass": {
		description: "WAF 绕过技法：编码/大小写/注释/分块/HTTP 层面的检测规避手法。",
		content:     wafBypassPrompt,
	},
	"nosql-injection": {
		description: "NoSQL 注入：MongoDB/Redis 等运算符注入、认证绕过与盲注提取。",
		content:     nosqlInjectionPrompt,
	},
	"type-juggling": {
		description: "类型混淆：PHP 松散比较、魔术哈希与 JSON 类型强制导致的认证/逻辑绕过。",
		content:     typeJugglingPrompt,
	},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./skills -count=1`
Expected: PASS（5 个名称/目录；三个新 Skill 可加载非空）。

- [ ] **Step 5: Commit**

```bash
gofmt -w skills/registry.go tests/_packages/skills/registry_test.go
git add skills/registry.go skills/waf-bypass/SKILL.md skills/nosql-injection/SKILL.md skills/type-juggling/SKILL.md tests/_packages/skills/registry_test.go
git commit -m "feat: migrate niche high-value skills into registry"
```

---

### Task 4: 全量验证与真实复测

- [ ] **Step 1: 全量回归**

Run:

```bash
go build ./...
go test ./...
go vet ./...
git diff --check
```

Expected: 全部通过；`go build`/`go vet` 无输出即成功。

- [ ] **Step 2: 构建并真实复测（授权 edusrc 目标）**

```bash
go build -o bin/pentgo ./cmd/pentgo
cd /tmp && rm -rf pentgo-verify && mkdir -p pentgo-verify && cd pentgo-verify
{ printf '对 https://lycvc.linyi.cn/ 进行授权 Web 安全评估：识别技术栈、发现可测试的参数与入口，对发现的问题给出可复现证据。\n'; sleep 300; printf '/quit\n'; } | timeout 320 /home/kali/PentGo/bin/pentgo > run.log 2>&1
```

- [ ] **Step 3: 核对四个可观测点**

在 `/tmp/pentgo-verify/eng-*/` 下确认：
1. **不限轮次**：engagement 不再因 `max_turns` 提前 `failed`；`session.json` 的 `stop_reason` 为 `task_complete`/`mission_complete`/`stuck`/`no_executable_response` 之一，而非 `max_turns`。
2. **模型声明标签入报告上下文**：`session.json` 时间线与 `report.md` 中，发现按模型标签（VERIFIED/LIKELY）分区，而非把每条成功命令都列为已验证。
3. **执行 Level 仍在**：`evidence/*.json` 仍带 `level`（执行成功度），与发现标签并存不冲突。
4. **Skill 可被加载**：任务允许时观察 `loaded_skills` 是否出现迁入的 Skill（非强制——取决于模型判断，但清单里现在有值得加载的项）。

保持非破坏性（仅 GET/探测级），受控速率，PoC 而非深度利用。

- [ ] **Step 4: Commit（若复测暴露需修项则补 commit，否则跳过）**

---

## 自查

- **Spec 覆盖**：max_turns=0 不限（Task 1）✓；模型自声明标签、与执行 Level 分离（Task 2）✓；迁移冷门 Skill（Task 3）✓；真实复测四点（Task 4）✓。
- **占位符扫描**：无 TODO/TBD；Skill 内容为文件复制（给出确切源/目标路径与命令，非占位）；registry/embed/测试断言均为完整代码。
- **类型一致性**：`extractFindingLabels`→`ReportTurn.DeclaredLabels`→`PromptText` 渲染（Task 2）一致；`EvidenceLevel` 常量在 Task 2 复用现有 `evidence_grade.go` 定义；`maxSkillBytes`/registry 条目/`Names`/`Catalog` 断言（Task 3）自洽为 5 项。

## 集成前提（执行者须先确认）

1. 各 `*_test.go` 为指向 `tests/_packages/` 的相对符号链接；新增测试先建真身再建链接。
2. Task 1 改默认 `MaxTurns=0` 后，配置归一化测试（`max_turns:0` 期望等于 `Default().Agent`）自动仍成立；仅第 15 行默认断言需从 20 改 0。
3. Task 3 的 `//go:embed` 目标文件必须先复制到位再编译，否则构建失败。
4. 去掉轮次硬顶后，务必保留 `hard_stuck_turns`/`no_code_limit`/取消等既有失控防护，本计划不削弱它们。
5. Task 4 真实复测仅限授权 edusrc 目标、非破坏性；主动利用类硬路径如需在真实目标进行须另行确认。
