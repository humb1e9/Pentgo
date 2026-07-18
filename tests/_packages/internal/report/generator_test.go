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
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	if !strings.Contains(client.requests[0].Messages[0].Content, "反幻觉审计") {
		t.Fatalf("audit section missing from request: %q", client.requests[0].Messages[0].Content)
	}
	if !strings.Contains(client.requests[0].SystemPrompt, "声明超过证据") {
		t.Fatalf("system prompt does not prioritize audit findings: %q", client.requests[0].SystemPrompt)
	}
}

func TestGenerateTerminalMarkdownUsesFrameworkVerdictsForClassification(t *testing.T) {
	client := &reportClient{response: agent.Response{Content: "# 最终报告\n"}}
	_, err := GenerateTerminalMarkdown(context.Background(), client, runtime.ReportContext{
		Target: "https://example.test",
		VerifiedFindings: []runtime.VerificationResult{{
			Verdict:    runtime.VerdictVerified,
			VulnType:   runtime.VulnXSS,
			Confidence: 0.95,
			Curl:       "curl -i -X GET 'https://example.test/?q=payload'",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	request := client.requests[0]
	for _, want := range []string{"框架验证发现", "Verdict=VERIFIED", "疑似发现", "INCONCLUSIVE/REFUTED"} {
		if !strings.Contains(request.SystemPrompt, want) {
			t.Fatalf("system prompt missing %q: %q", want, request.SystemPrompt)
		}
	}
	for _, want := range []string{"框架已验证", "VERDICT: VERIFIED", "curl -i -X GET"} {
		if !strings.Contains(request.Messages[0].Content, want) {
			t.Fatalf("report context missing %q: %q", want, request.Messages[0].Content)
		}
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
