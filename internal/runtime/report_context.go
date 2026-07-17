package runtime

import (
	"fmt"
	"strings"
)

const (
	maxReportContextBytes      = 16 * 1024
	maxReportBlockOutputBytes  = 1200
	reportContextOmittedMarker = "\n[additional execution summaries omitted]\n"
)

// ReportBlock 是可发送给报告模型的单个代码块执行摘要。
type ReportBlock struct {
	Index        int
	Language     Language
	Status       ExecutionStatus
	ExitCode     int
	Stdout       string
	Stderr       string
	EvidencePath string
	Level        EvidenceLevel
}

// ReportTurn 是单个 Agent 回合的无代码报告摘要。
type ReportTurn struct {
	Number         int
	Decision       string
	DeclaredLabels []EvidenceLevel
	Blocks         []ReportBlock
}

// ReportContext 是生成最终报告所需的有界执行上下文。
type ReportContext struct {
	Target         string
	Intent         string
	Status         SessionStatus
	StopReason     string
	Skills         []string
	Turns          []ReportTurn
	RecoveryEvents []TimelineEvent
}

// PromptText 渲染不包含模型源码的报告证据文本。
func (context ReportContext) PromptText() string {
	var builder strings.Builder
	appendReportText(&builder, fmt.Sprintf("目标: %s\n任务: %s\n会话状态: %s\n停止原因: %s\n", context.Target, context.Intent, context.Status, context.StopReason))
	if len(context.Skills) > 0 {
		appendReportText(&builder, "已加载 Skill: "+strings.Join(context.Skills, ", ")+"\n")
	}
	for _, turn := range context.Turns {
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
		for _, block := range turn.Blocks {
			metadata := fmt.Sprintf("块 %d\n语言: %s\n状态: %s\n退出码: %d\n执行等级: %s\nevidence: %s\n", block.Index, block.Language, block.Status, block.ExitCode, block.Level, block.EvidencePath)
			if !appendReportText(&builder, metadata) {
				return finishReportPrompt(&builder)
			}
			if block.Stdout != "" && !appendReportText(&builder, "stdout 摘要:\n"+block.Stdout+"\n") {
				return finishReportPrompt(&builder)
			}
			if block.Stderr != "" && !appendReportText(&builder, "stderr 摘要:\n"+block.Stderr+"\n") {
				return finishReportPrompt(&builder)
			}
		}
	}
	if len(context.RecoveryEvents) > 0 {
		if !appendReportText(&builder, "\n恢复事件:\n") {
			return finishReportPrompt(&builder)
		}
		for _, event := range context.RecoveryEvents {
			if !appendReportText(&builder, fmt.Sprintf("- 回合 %d: %s %s\n", event.Turn, event.Kind, event.Detail)) {
				return finishReportPrompt(&builder)
			}
		}
	}
	return builder.String()
}

func appendReportText(builder *strings.Builder, value string) bool {
	remaining := maxReportContextBytes - builder.Len()
	if remaining <= 0 {
		return false
	}
	if len(value) <= remaining {
		builder.WriteString(value)
		return true
	}
	builder.WriteString(truncateBytes(value, remaining))
	return false
}

func finishReportPrompt(builder *strings.Builder) string {
	if remaining := maxReportContextBytes - builder.Len(); remaining >= len(reportContextOmittedMarker) {
		builder.WriteString(reportContextOmittedMarker)
	}
	return builder.String()
}
