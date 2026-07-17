package report

import (
	"context"
	"errors"
	"strings"

	"pentgo/internal/agent"
	"pentgo/internal/runtime"
)

const terminalReportSystemPrompt = `你是 PentGo 的最终报告撰写器。生成中文 Markdown 渗透测试报告。

只能使用用户消息中提供的执行证据。不要运行代码，不要提供新的命令，不要编造漏洞、凭据、影响或复现结果。没有直接执行证据支持的漏洞必须写为“未验证漏洞”，不能将服务指纹、HTTP 状态或模型推断标记为漏洞。发现强度以“模型声明标签”（VERIFIED/LIKELY/INFERRED）为准：只有 VERIFIED、LIKELY 且有对应执行证据的项列入“已验证发现”；INFERRED 或无执行证据支撑的项归入“未完成或受阻项目”。代码块的执行状态仅为技术旁证，不等于漏洞被证实。

使用以下二级章节：
## 目标与范围
## 执行摘要
## 已验证发现
## 证据索引
## 影响与修复建议
## 未完成或受阻项目

只输出报告正文，不使用代码块。`

// GenerateTerminalMarkdown 通过独立模型调用生成最终 Markdown 报告。
func GenerateTerminalMarkdown(ctx context.Context, client agent.Client, reportContext runtime.ReportContext) (string, error) {
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
