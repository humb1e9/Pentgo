# 反幻觉验证层实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在报告模型调用前加一道**纯确定性预验证**，把模型声明的发现标签（`DeclaredLabels`）与同一回合的实际执行证据等级（`ReportBlock.Level`）交叉比对。比对结果作为预计算审计摘要注入报告上下文，使报告模型在已验证事实基础上撰写，而非仅凭指令约束自己。

## 架构分析（基于实测的读码结论，非推测）

### bingo 做了什么、PentGo 为何不能照搬

bingo `anti_hallucination.py` 的 `ZeroHallucinationGuard` 要求模型**直接调用 Python API**（`add_http_finding(method=, url=, status_code=, response_body=, …)`）并传入真实 HTTP 证据对象。这在 bingo 的 Python-native 架构里可行——模型生成的 Python 代码块可以调用框架 API。PentGo 中模型只能生成任意代码块由 executor 执行，无法调用 Go API，因此 bingo 方案**不可直接移植**。

### PentGo 现有基础（已就位，非缺失）

| 组件 | 位置 | 作用 |
|---|---|---|
| `EvidenceLevel` (VERIFIED/LIKELY/INFERRED) | `evidence_grade.go` | 执行结果质量等级（基于 exit code + stdout/stderr）|
| `GradeEvidence()` | `evidence_grade.go` | 为每个块的执行结果定级 |
| `extractFindingLabels()` | `finding_label.go` | 从模型文本提取 `[VERIFIED]`/`[LIKELY]`/`[INFERRED]` |
| `ReportTurn.DeclaredLabels` | `report_context.go` | 模型本轮声明的发现等级（turn 级别）|
| `ReportBlock.Level` | `report_context.go` | 执行测量等级（block 级别）|
| `ReportContext.PromptText()` | `report_context.go` | 生成发给报告模型的上下文文本 |
| `GenerateTerminalMarkdown(ctx, client, ReportContext)` | `report/generator.go` | 独立模型调用生成报告 |
| 报告系统提示 | `report/generator.go:13` | 已要求报告模型按证据分级归档，但属于模型指令，非确定性门控 |
| 集成点 | `app/engagement.go:129` | `runner.ReportContext(session)` → `GenerateTerminalMarkdown` |

### 真正的缺口

`DeclaredLabels`（turn 级）与 `Blocks[].Level`（block 级）**存在于同一 `ReportTurn` 结构中，但没有任何代码对它们做交叉核验**。

发生场景举例：
- 模型写了一段探测脚本（exit code 1，仅有 stderr）→ `Level = INFERRED`
- 同一回合文本写了 `[VERIFIED] SQL 注入确认` → `DeclaredLabels = [VERIFIED]`
- 现状：报告模型收到两组数据，需自行判断；实测经常按声明而非执行结果撰写

预验证的价值：**把"声明超过证据"变成已计算的显式事实**，不再让报告模型自行推断。

---

## 设计

**`ValidatedReportContext`**（新增，嵌入 `ReportContext`）

```
ValidatedReportContext
  ├── ReportContext               (embed)
  ├── TurnValidations []TurnValidation
  │     ├── Turn int
  │     ├── DeclaredMax  EvidenceLevel   // 模型声明的最高等级
  │     ├── EvidenceMax  EvidenceLevel   // 执行测量的最高等级
  │     ├── ClaimExceeds bool            // DeclaredMax > EvidenceMax
  │     └── HasExecution bool            // 本回合是否有块执行
  ├── ClaimsExceedingEvidence int        // 有声明超出证据的回合数
  └── TurnsWithExecution      int        // 有执行证据的回合数
```

**等级排名**（用于比较）：`VERIFIED=2 > LIKELY=1 > INFERRED=0`。

**`ValidatedReportContext.PromptText()`**：调用嵌入的 `ReportContext.PromptText()` 后追加审计摘要节，例如：

```
反幻觉审计:
回合 3: 模型声明 VERIFIED, 最高执行等级 INFERRED — 声明超过证据
回合 7: 模型声明 LIKELY, 最高执行等级 INFERRED — 声明超过证据
超过证据的声明: 2 回合 / 总计 9 回合
有执行证据的回合: 6 / 9
```

**`GenerateTerminalMarkdown`** 改为接受 `PromptContexter` 接口（`{ PromptText() string }`），使 `ReportContext`（原有测试不改）和 `ValidatedReportContext` 都能传入（duck typing，无额外导入）。

**不做的事**：
- 不额外发起模型调用（零 API 成本，避免验证器本身幻觉）
- 不阻止或丢弃任何发现（只标注，不过滤）
- 不修改 runner 主循环（所有信息已在 ReportContext 里）

---

## Global Constraints

- 模块名 `pentgo`；Go 版本 `go 1.25.0`。
- 生产源码放 `internal/runtime/` 或 `internal/report/` 或 `internal/app/`；测试真身放 `tests/_packages/<same-rel-path>/`，源码目录建**相对符号链接**（`<file>_test.go -> ../../tests/_packages/.../<file>_test.go`）。
- 测试 package 与被测包同名，不使用外部 `_test` 包。
- 改动签名时**不改现有测试的 pass 路径**（`generator_test.go` 中的 `runtime.ReportContext` 仍可作为 `PromptContexter` 使用，因 `PromptText()` 方法已存在）。
- 每步结束运行该步列出的测试；每个 Task 至少一次提交。

---

### Task 1: `ValidatedReportContext` 与 `ValidateReportContext()`（TDD）

**Files:**
- Create: `internal/runtime/validation.go`
- Create: `tests/_packages/internal/runtime/validation_test.go`
- Create symlink: `internal/runtime/validation_test.go -> ../../tests/_packages/internal/runtime/validation_test.go`

**Interfaces:**
- Produces:
  - `type TurnValidation struct { Turn int; DeclaredMax, EvidenceMax EvidenceLevel; ClaimExceeds, HasExecution bool }`
  - `type ValidatedReportContext struct { ReportContext; TurnValidations []TurnValidation; ClaimsExceedingEvidence, TurnsWithExecution int }`
  - `func ValidateReportContext(rc ReportContext) ValidatedReportContext`
  - `func (v ValidatedReportContext) PromptText() string`
  - `func levelRank(level EvidenceLevel) int` (unexported)

- [ ] **Step 1: Write the failing tests**

`tests/_packages/internal/runtime/validation_test.go`（创建，含以下测试）：

```go
package runtime

import (
    "strings"
    "testing"
)

func TestValidateReportContextNoTurns(t *testing.T) {
    rc := ReportContext{Target: "https://example.test", Intent: "test"}
    v := ValidateReportContext(rc)
    if v.ClaimsExceedingEvidence != 0 || v.TurnsWithExecution != 0 {
        t.Fatalf("empty rc = %+v", v)
    }
    if v.Target != rc.Target {
        t.Fatalf("embedded rc not preserved")
    }
}

func TestValidateReportContextClaimExceedsEvidence(t *testing.T) {
    rc := ReportContext{
        Turns: []ReportTurn{
            {
                Number:         1,
                DeclaredLabels: []EvidenceLevel{EvidenceVerified},
                Blocks: []ReportBlock{
                    {Level: EvidenceInferred, Status: ExecutionFailed},
                },
            },
        },
    }
    v := ValidateReportContext(rc)
    if len(v.TurnValidations) != 1 {
        t.Fatalf("TurnValidations len = %d", len(v.TurnValidations))
    }
    tv := v.TurnValidations[0]
    if !tv.ClaimExceeds {
        t.Fatalf("ClaimExceeds = false, want true (VERIFIED declared, INFERRED evidence)")
    }
    if tv.DeclaredMax != EvidenceVerified || tv.EvidenceMax != EvidenceInferred {
        t.Fatalf("DeclaredMax/EvidenceMax = %s/%s", tv.DeclaredMax, tv.EvidenceMax)
    }
    if v.ClaimsExceedingEvidence != 1 {
        t.Fatalf("ClaimsExceedingEvidence = %d", v.ClaimsExceedingEvidence)
    }
    if v.TurnsWithExecution != 1 {
        t.Fatalf("TurnsWithExecution = %d", v.TurnsWithExecution)
    }
}

func TestValidateReportContextSupportedClaim(t *testing.T) {
    rc := ReportContext{
        Turns: []ReportTurn{
            {
                Number:         2,
                DeclaredLabels: []EvidenceLevel{EvidenceLikely},
                Blocks: []ReportBlock{
                    {Level: EvidenceVerified, Status: ExecutionSucceeded},
                },
            },
        },
    }
    v := ValidateReportContext(rc)
    if v.ClaimsExceedingEvidence != 0 {
        t.Fatalf("ClaimsExceedingEvidence = %d, want 0 (LIKELY declared, VERIFIED evidence — claim OK)", v.ClaimsExceedingEvidence)
    }
    if !v.TurnValidations[0].HasExecution {
        t.Fatalf("HasExecution = false")
    }
}

func TestValidateReportContextNoDeclaredLabels(t *testing.T) {
    rc := ReportContext{
        Turns: []ReportTurn{
            {
                Number: 3,
                Blocks: []ReportBlock{{Level: EvidenceVerified}},
            },
        },
    }
    v := ValidateReportContext(rc)
    if v.ClaimsExceedingEvidence != 0 {
        t.Fatalf("no declared labels should not trigger ClaimExceeds")
    }
    if v.TurnValidations[0].ClaimExceeds {
        t.Fatalf("ClaimExceeds = true with no DeclaredLabels")
    }
}

func TestValidateReportContextTurnNoBlocks(t *testing.T) {
    rc := ReportContext{
        Turns: []ReportTurn{
            {
                Number:         4,
                DeclaredLabels: []EvidenceLevel{EvidenceVerified},
            },
        },
    }
    v := ValidateReportContext(rc)
    if !v.TurnValidations[0].ClaimExceeds {
        t.Fatalf("VERIFIED declared with no blocks should ClaimExceeds = true")
    }
    if v.TurnValidations[0].HasExecution {
        t.Fatalf("HasExecution should be false with no blocks")
    }
}

func TestValidatedReportContextPromptTextIncludesAuditSection(t *testing.T) {
    rc := ReportContext{
        Target: "https://example.test",
        Turns: []ReportTurn{
            {
                Number:         1,
                DeclaredLabels: []EvidenceLevel{EvidenceVerified},
                Blocks:         []ReportBlock{{Level: EvidenceInferred}},
            },
        },
    }
    v := ValidateReportContext(rc)
    text := v.PromptText()
    if !strings.Contains(text, "反幻觉审计") {
        t.Fatalf("PromptText missing audit section: %q", text[:min(200, len(text))])
    }
    if !strings.Contains(text, "声明超过证据") {
        t.Fatalf("PromptText missing claim-exceeds notice")
    }
    // must also include base context
    if !strings.Contains(text, "https://example.test") {
        t.Fatalf("base ReportContext missing from PromptText")
    }
}

func min(a, b int) int {
    if a < b { return a }
    return b
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/runtime -run "TestValidate" -timeout 30s -count=1`
Expected: FAIL（`ValidateReportContext`、`ValidatedReportContext` 未定义）。

- [ ] **Step 3: Write minimal implementation**

`internal/runtime/validation.go`:

```go
package runtime

import (
    "fmt"
    "strings"
)

// TurnValidation 记录单个回合中模型声明等级与执行证据等级的交叉核验结果。
type TurnValidation struct {
    Turn         int
    DeclaredMax  EvidenceLevel
    EvidenceMax  EvidenceLevel
    ClaimExceeds bool // DeclaredMax 高于 EvidenceMax
    HasExecution bool // 本回合至少有一个块执行
}

// ValidatedReportContext 是追加了反幻觉审计注释的报告上下文。
type ValidatedReportContext struct {
    ReportContext
    TurnValidations         []TurnValidation
    ClaimsExceedingEvidence int // 声明超过证据的回合数
    TurnsWithExecution      int // 有执行证据的回合数
}

// ValidateReportContext 纯确定性：交叉核验每个回合的模型声明与执行证据，不发起任何网络请求。
func ValidateReportContext(rc ReportContext) ValidatedReportContext {
    validations := make([]TurnValidation, 0, len(rc.Turns))
    claimsExceeding := 0
    turnsWithExec := 0

    for _, turn := range rc.Turns {
        tv := TurnValidation{Turn: turn.Number}

        // 计算执行证据最高等级
        tv.EvidenceMax = EvidenceInferred
        for _, block := range turn.Blocks {
            tv.HasExecution = true
            if levelRank(block.Level) > levelRank(tv.EvidenceMax) {
                tv.EvidenceMax = block.Level
            }
        }
        if tv.HasExecution {
            turnsWithExec++
        }

        // 计算模型声明最高等级
        tv.DeclaredMax = EvidenceInferred
        for _, label := range turn.DeclaredLabels {
            if levelRank(label) > levelRank(tv.DeclaredMax) {
                tv.DeclaredMax = label
            }
        }

        // 仅当模型有声明时才判断超出
        if len(turn.DeclaredLabels) > 0 && levelRank(tv.DeclaredMax) > levelRank(tv.EvidenceMax) {
            tv.ClaimExceeds = true
            claimsExceeding++
        }
        validations = append(validations, tv)
    }

    return ValidatedReportContext{
        ReportContext:           rc,
        TurnValidations:         validations,
        ClaimsExceedingEvidence: claimsExceeding,
        TurnsWithExecution:      turnsWithExec,
    }
}

// PromptText 在基础报告上下文之后追加反幻觉审计摘要。
func (v ValidatedReportContext) PromptText() string {
    base := v.ReportContext.PromptText()
    var sb strings.Builder
    sb.WriteString(base)
    sb.WriteString("\n反幻觉审计:\n")
    for _, tv := range v.TurnValidations {
        if tv.ClaimExceeds {
            sb.WriteString(fmt.Sprintf("回合 %d: 模型声明 %s, 最高执行等级 %s — 声明超过证据\n",
                tv.Turn, tv.DeclaredMax, tv.EvidenceMax))
        }
    }
    sb.WriteString(fmt.Sprintf("超过证据的声明: %d 回合 / 总计 %d 回合\n",
        v.ClaimsExceedingEvidence, len(v.TurnValidations)))
    sb.WriteString(fmt.Sprintf("有执行证据的回合: %d / %d\n",
        v.TurnsWithExecution, len(v.TurnValidations)))
    return sb.String()
}

// levelRank 返回证据等级的数值排名（越高越可信）。
func levelRank(level EvidenceLevel) int {
    switch level {
    case EvidenceVerified:
        return 2
    case EvidenceLikely:
        return 1
    default:
        return 0
    }
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/runtime -run "TestValidate" -timeout 30s -count=1`
再跑整包确认无回归：`go test ./internal/runtime -timeout 60s -count=1`
Expected: 均 PASS。

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/runtime/validation.go tests/_packages/internal/runtime/validation_test.go
git add internal/runtime/validation.go internal/runtime/validation_test.go tests/_packages/internal/runtime/validation_test.go
git commit -m "feat: add ValidateReportContext for anti-hallucination pre-report audit"
```

---

### Task 2: `report.GenerateTerminalMarkdown` 接受 `PromptContexter` 接口

**Files:**
- Modify: `internal/report/generator.go`
- Modify: `tests/_packages/internal/report/generator_test.go`（追加一条测试；现有不改）

**Interfaces:**
- Adds: `type PromptContexter interface { PromptText() string }`（report 包内，duck typing，无需导入 runtime）
- Changes: `GenerateTerminalMarkdown(ctx, client, PromptContexter)` 签名
- Existing `runtime.ReportContext` 已有 `PromptText()`，满足接口 → 现有测试**编译不改直接通过**
- New `ValidatedReportContext` 同样满足接口

- [ ] **Step 1: Write failing test（确认 ValidatedReportContext 可传入 generator）**

追加到 `tests/_packages/internal/report/generator_test.go`：

```go
func TestGenerateTerminalMarkdownAcceptsValidatedReportContext(t *testing.T) {
    client := &reportClient{response: agent.Response{Content: "# 最终报告\n\n## 已验证发现\n未验证漏洞。"}}
    validated := runtime.ValidateReportContext(runtime.ReportContext{
        Target: "https://example.test",
        Turns: []runtime.ReportTurn{{
            Number:         1,
            Decision:       "探测首页",
            DeclaredLabels: []runtime.EvidenceLevel{runtime.EvidenceVerified},
            Blocks: []runtime.ReportBlock{{
                Level:  runtime.EvidenceInferred,
                Status: runtime.ExecutionFailed,
            }},
        }},
    })
    markdown, err := GenerateTerminalMarkdown(context.Background(), client, validated)
    if err != nil || markdown == "" {
        t.Fatalf("markdown/err = %q/%v", markdown, err)
    }
    // 传入的 prompt 应含审计信息
    if !strings.Contains(client.requests[0].Messages[0].Content, "反幻觉审计") {
        t.Fatalf("audit section not found in request: %q", client.requests[0].Messages[0].Content[:min(300, len(client.requests[0].Messages[0].Content))])
    }
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

Run: `go test ./internal/report -run TestGenerateTerminalMarkdownAcceptsValidatedReportContext -timeout 30s -count=1`
Expected: FAIL（`GenerateTerminalMarkdown` 还不接受 `ValidatedReportContext`）。

- [ ] **Step 2: Modify `generator.go`**

在 `GenerateTerminalMarkdown` 之前添加接口：

```go
// PromptContexter 是任何能提供报告上下文文本的类型。
// 由 runtime.ReportContext 和 runtime.ValidatedReportContext 均满足。
type PromptContexter interface {
    PromptText() string
}
```

修改函数签名：

```go
// 改前:
func GenerateTerminalMarkdown(ctx context.Context, client agent.Client, reportContext runtime.ReportContext) (string, error) {

// 改后:
func GenerateTerminalMarkdown(ctx context.Context, client agent.Client, reportContext PromptContexter) (string, error) {
```

函数体内原来的 `reportContext.PromptText()` 不变（接口已有该方法）。

- [ ] **Step 3: Run to verify it passes**

Run: `go test ./internal/report -timeout 30s -count=1`
Expected: 所有测试 PASS（现有的 `runtime.ReportContext` 传参自动满足接口；新测试通过）。

- [ ] **Step 4: Commit**

```bash
gofmt -w internal/report/generator.go tests/_packages/internal/report/generator_test.go
git add internal/report/generator.go tests/_packages/internal/report/generator_test.go
git commit -m "refactor: GenerateTerminalMarkdown accepts PromptContexter interface"
```

---

### Task 3: `app/engagement.go` 接线

**Files:**
- Modify: `internal/app/engagement.go`
- Test: 现有 `tests/_packages/internal/app/` 全量回归即可（无需新 app-level 测试，已由 Task 1/2 的单元测试覆盖验证逻辑）

**Change:** `engagement.go:129` 处：

```go
// 改前:
markdown, reportErr := report.GenerateTerminalMarkdown(ctx, client, runner.ReportContext(session))

// 改后:
validated := runtime.ValidateReportContext(runner.ReportContext(session))
markdown, reportErr := report.GenerateTerminalMarkdown(ctx, client, validated)
```

确认 import `runtime` 已存在（已有，见 engagement.go:17）。

- [ ] **Step 1: Apply the two-line change**

- [ ] **Step 2: Build + run app tests**

```bash
go build ./...
go test ./internal/app -timeout 30s -count=1
```

Expected: build OK，tests PASS。

- [ ] **Step 3: Commit**

```bash
gofmt -w internal/app/engagement.go
git add internal/app/engagement.go
git commit -m "feat: wire ValidateReportContext into engagement report pipeline"
```

---

### Task 4: 全量验证

- [ ] **Step 1: Full regression**

```bash
go build ./...
go test ./... -timeout 120s
go vet ./...
git diff --check
```

Expected: 全部通过。

- [ ] **Step 2: 端对端冒烟（可选，若有授权目标）**

跑一次真实 engagement，确认最终 `report.md` 的"反幻觉审计"节包含正确的回合统计。

---

## 自查

- **Spec 覆盖**：`ValidatedReportContext` 预验证（Task 1）✓；接口解耦（Task 2）✓；接线（Task 3）✓；全量验证（Task 4）✓。
- **占位符扫描**：无 TODO/TBD；所有 Go 代码段完整可编译；测试名称与断言对应。
- **类型一致性**：`ValidatedReportContext.PromptText()` 满足 `PromptContexter`；`ReportContext.PromptText()` 已有方法满足同接口；engagement.go 中 `runtime.ValidateReportContext` 调用不引入新 import。
- **行为不变**：Task 2 的接口抽象对现有测试透明（duck typing）；Task 3 仅在同一位置插两行，其余流程不改。
- **零成本**：`ValidateReportContext` 纯确定性，不发起任何 I/O 或模型调用。

## 集成前提

1. `validation_test.go` 符号链接须在 `internal/runtime/` 目录创建（链接真身位于 `tests/_packages/internal/runtime/validation_test.go`），与现有 runner_test.go 等模式一致。
2. Task 1 中 `min()` helper 已在 validation_test.go 定义；若 runner_test.go 等同包文件也定义了 `min()`，改名为 `minInt()` 避免重复定义。
3. Task 2 追加测试时同理检查 `min()` 是否已在 generator_test.go 中定义。
4. Task 3 两行变更依赖 Task 1 和 Task 2 都已完成；按顺序执行。
