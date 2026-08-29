package model

import "strings"

// baseSystemPrompt 包含固定指令；每轮的运行上下文和共享事实由 SystemPrompt 追加。
const baseSystemPrompt = `你是 **PentGo 渗透测试智能体**，仅对用户明确授权的目标进行安全测试，使用中文回答。

### 任务与目标

根据用户的测试目标，选择合适的 Skill 和工具完成安全测试，并基于实际证据输出可复核的测试结论。

### 范围与约束

**模糊请求**：测试目标或范围不明确时，不得直接执行；列出可选测试方向、目的、前置条件和风险，等待用户指定。
**明确请求**：针对明确的测试目标，制定分阶段、低风险、可复核的测试步骤。

### Skill 与工具

根据任务选择并加载匹配的 Skill，遵循 Skill 定义的测试方法；根据当前任务选择匹配的工具，并严格遵守工具 Schema。

### 结果汇报

测试结束时，用中文区分：

**已验证内容**
**关键证据**
**安全发现**
**待验证项**

工具调用结果必须保留 ` + "`" + `evidence_ref` + "`" + `，不得伪造测试结果或证据。`

// BaseSystemPrompt returns the stable instruction envelope used by every model
// request. Context preflight combines it with the fixed project-facts framing.
func BaseSystemPrompt() string { return baseSystemPrompt }

// ProjectFactsEnvelope wraps the exact project-facts section appended to the
// provider system message. It is exported so context measurement counts the
// same fixed text that SystemPrompt sends.
func ProjectFactsEnvelope(projectFacts string) string {
	if strings.TrimSpace(projectFacts) == "" {
		projectFacts = "当前没有记录项目事实。"
	}
	return "\n\n项目共享事实（只提供上下文，不会扩大当前会话范围）：\n" + strings.TrimSpace(projectFacts)
}

// SystemInstructionPrefix returns the final instruction section before project
// facts. It accepts a previously assembled prefix so hosts can meter precisely
// the same envelope that StreamStep will send.
func SystemInstructionPrefix(input string) string {
	input = strings.TrimSpace(input)
	if input == "" || input == baseSystemPrompt {
		return baseSystemPrompt
	}
	if strings.HasPrefix(input, baseSystemPrompt+"\n\n当前运行上下文：\n") {
		return input
	}
	return baseSystemPrompt + "\n\n当前运行上下文：\n" + input
}

// SystemPrompt combines the provider-visible instruction prefix and the exact
// project-facts envelope. Context preflight measures these two parts directly.
func SystemPrompt(input string, projectFacts string) string {
	return SystemInstructionPrefix(input) + ProjectFactsEnvelope(projectFacts)
}
