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
// request. Runtime context and shared facts are appended by SystemPrompt.
func BaseSystemPrompt() string { return baseSystemPrompt }

// factsEnvelope 是共享事实在指令中的固定段。事实内容本身由 ContextMiddleware
// 作为逐轮系统消息注入，此处恒为占位文本。
const factsEnvelope = "\n\n项目共享事实（只提供上下文，不会扩大当前会话范围）：\n当前没有记录项目事实。"

// SystemPrompt assembles the provider instruction from the base prompt plus any
// per-run runtime context, then appends the fixed project-facts envelope.
func SystemPrompt(input string) string {
	input = strings.TrimSpace(input)
	prefix := input
	if input == "" || input == baseSystemPrompt {
		prefix = baseSystemPrompt
	} else if !strings.HasPrefix(input, baseSystemPrompt+"\n\n当前运行上下文：\n") {
		prefix = baseSystemPrompt + "\n\n当前运行上下文：\n" + input
	}
	return prefix + factsEnvelope
}
