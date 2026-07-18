package report

import (
	"context"
	"errors"
	"strings"

	"pentgo/internal/agent"
)

const terminalReportSystemPrompt = `你是 PentGo 的最终报告撰写器。生成中文 Markdown 渗透测试报告。

只使用用户消息中提供的执行证据。框架会在最终发布时确定性渲染“已验证发现”章节，含 Verdict、置信度、curl 与证据路径；你仅撰写叙述性章节，勿重复或重分类框架 findings。不得运行代码、提供新命令或编造漏洞、凭据、影响、复现结果。模型声明标签、服务指纹、HTTP 状态和代码块执行状态均不得提升为已验证漏洞。反幻觉审计标记为“声明超过证据”的回合必须归入“未完成或受阻项目”。

使用以下二级章节：
## 目标与范围
## 执行摘要
## 影响与修复建议
## 未完成或受阻项目

只输出报告正文，不使用代码块。`

// PromptContexter is any value that can render report evidence for the model.
type PromptContexter interface {
	PromptText() string
}

// GenerateTerminalMarkdown 通过独立模型调用生成最终 Markdown 报告。
func GenerateTerminalMarkdown(ctx context.Context, client agent.Client, reportContext PromptContexter) (string, error) {
	if client == nil {
		return "", errors.New("nil report client")
	}
	response, err := client.Chat(ctx, agent.Request{
		SystemPrompt: terminalReportSystemPrompt,
		Messages: []agent.Message{{
			Role:    "user",
			Content: reportContext.PromptText(),
		}},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Content) == "" {
		return "", errors.New("report model returned empty content")
	}
	return response.Content, nil
}
