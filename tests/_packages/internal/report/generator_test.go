package report

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/runtime"
)

func TestGenerateTerminalMarkdownUsesIndependentEvidenceOnlyRequest(t *testing.T) {
	client := &reportClient{response: agent.Response{Content: "# 最终报告\n\n## 已验证发现\n未验证漏洞。"}}
	markdown, err := GenerateTerminalMarkdown(context.Background(), client, runtime.ReportContext{
		Target: "https://example.test",
		Turns: []runtime.ReportTurn{{
			Number:   1,
			Decision: "检查首页",
			Blocks: []runtime.ReportBlock{{
				Status:       runtime.ExecutionSucceeded,
				EvidencePath: "evidence/agent-turn-001-block-001.json",
			}},
		}},
	})
	if err != nil || markdown != "# 最终报告\n\n## 已验证发现\n未验证漏洞。" || len(client.requests) != 1 {
		t.Fatalf("markdown/err/requests = %q/%v/%d", markdown, err, len(client.requests))
	}
	request := client.requests[0]
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" || strings.Contains(request.Messages[0].Content, "```python") {
		t.Fatalf("request messages = %+v", request.Messages)
	}
	for _, section := range []string{"目标与范围", "执行摘要", "已验证发现", "证据索引", "影响与修复建议", "未完成或受阻项目"} {
		if !strings.Contains(request.SystemPrompt, section) {
			t.Fatalf("system prompt missing %q: %q", section, request.SystemPrompt)
		}
	}
}

func TestGenerateTerminalMarkdownRejectsEmptyModelResponse(t *testing.T) {
	_, err := GenerateTerminalMarkdown(context.Background(), &reportClient{}, runtime.ReportContext{})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestPublishWithReportWritesModelMarkdown(t *testing.T) {
	session := artifactTestSession(t)
	writer, err := NewEngagementWriter(t.TempDir(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	artifacts, err := writer.PublishWithReport(session, time.Now().UTC(), "# 模型报告\n")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(artifacts.Markdown)
	if err != nil || string(body) != "# 模型报告\n" {
		t.Fatalf("body/err = %q/%v", body, err)
	}
}

func TestPublishWithReportFallsBackForEmptyMarkdown(t *testing.T) {
	session := artifactTestSession(t)
	writer, err := NewEngagementWriter(t.TempDir(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	artifacts, err := writer.PublishWithReport(session, time.Now().UTC(), "  ")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(artifacts.Markdown)
	if err != nil || !strings.Contains(string(body), "## Execution Timeline") {
		t.Fatalf("body/err = %q/%v", body, err)
	}
}

type reportClient struct {
	response agent.Response
	err      error
	requests []agent.Request
}

func (client *reportClient) Chat(_ context.Context, request agent.Request) (agent.Response, error) {
	client.requests = append(client.requests, request)
	if client.err != nil {
		return agent.Response{}, client.err
	}
	return client.response, nil
}
