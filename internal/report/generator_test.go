package report

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/runtime/exec"
	"pentgo/internal/runtime/loop"
	"pentgo/internal/runtime/verify"
)

func TestGenerateTerminalMarkdownUsesIndependentEvidenceOnlyRequest(t *testing.T) {
	client := &reportClient{response: agent.Response{Content: "## 执行摘要\n已完成执行。"}}
	markdown, err := GenerateTerminalMarkdown(context.Background(), client, loop.ReportContext{
		Target: "https://example.test",
		Turns: []loop.ReportTurn{{
			Number:   1,
			Decision: "检查首页",
			Blocks: []loop.ReportBlock{{
				Status:       exec.ExecutionSucceeded,
				EvidencePath: "evidence/agent-turn-001-block-001.json",
			}},
		}},
	})
	if err != nil || markdown != "## 执行摘要\n已完成执行。" || len(client.requests) != 1 {
		t.Fatalf("markdown/err/requests = %q/%v/%d", markdown, err, len(client.requests))
	}
	request := client.requests[0]
	if len(request.Messages) != 1 || request.Messages[0].Role != "user" || strings.Contains(request.Messages[0].Content, "```python") {
		t.Fatalf("request messages = %+v", request.Messages)
	}
	for _, section := range []string{"目标与范围", "执行摘要", "影响与修复建议", "未完成或受阻项目", "确定性渲染"} {
		if !strings.Contains(request.SystemPrompt, section) {
			t.Fatalf("system prompt missing %q: %q", section, request.SystemPrompt)
		}
	}
}

func TestGenerateTerminalMarkdownRejectsEmptyModelResponse(t *testing.T) {
	_, err := GenerateTerminalMarkdown(context.Background(), &reportClient{}, loop.ReportContext{})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestGenerateTerminalMarkdownAcceptsValidatedReportContext(t *testing.T) {
	client := &reportClient{response: agent.Response{Content: "## 执行摘要\n已完成执行。"}}
	validated := loop.ValidateReportContext(loop.ReportContext{
		Target: "https://example.test",
		Turns: []loop.ReportTurn{{
			Number:         1,
			Decision:       "探测首页",
			DeclaredLabels: []exec.EvidenceLevel{exec.EvidenceVerified},
			Blocks: []loop.ReportBlock{{
				Level:  exec.EvidenceInferred,
				Status: exec.ExecutionFailed,
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

func TestGenerateTerminalMarkdownSeparatesNarrativeFromFrameworkFindings(t *testing.T) {
	client := &reportClient{response: agent.Response{Content: "## 执行摘要\n已完成执行。"}}
	_, err := GenerateTerminalMarkdown(context.Background(), client, loop.ReportContext{
		Target: "https://example.test",
		VerifiedFindings: []verify.VerificationResult{{
			Verdict:    verify.VerdictVerified,
			VulnType:   verify.VulnXSS,
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
	for _, want := range []string{"确定性渲染", "仅撰写叙述性章节", "勿重复"} {
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

func TestRenderVerifiedFindingsClassifiesFrameworkVerdicts(t *testing.T) {
	markdown := RenderVerifiedFindings([]verify.VerificationResult{
		{
			Verdict:      verify.VerdictVerified,
			VulnType:     verify.VulnXSS,
			Confidence:   0.95,
			Summary:      "payload reflected verbatim",
			Curl:         "curl -i -X GET 'https://example.test/?q=payload'",
			EvidencePath: "evidence/verification-001.json",
		},
		{
			Verdict:    verify.VerdictInconclusive,
			VulnType:   verify.VulnSQLI,
			Confidence: 0.40,
			Summary:    "no causal difference",
		},
	})
	for _, want := range []string{"## 已验证发现", "### 确认漏洞", "XSS", "curl -i -X GET", "evidence/verification-001.json", "### 声明未获框架验证", "INCONCLUSIVE"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q: %q", want, markdown)
		}
	}
}

func TestRenderVerifiedFindingsRendersCredentialLoginMetadata(t *testing.T) {
	markdown := RenderVerifiedFindings([]verify.VerificationResult{{
		Verdict:          verify.VerdictVerified,
		VulnType:         verify.VulnCredential,
		Confidence:       0.80,
		LoginVerified:    true,
		LoginCookieNames: []string{"sid", "csrf"},
		Username:         "fixture",
	}})
	for _, want := range []string{"Login verified: true", "Session cookies: sid, csrf", "Username: fixture"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q: %q", want, markdown)
		}
	}
}

func TestRenderVerifiedFindingsMarksEmptyInputAsUnverified(t *testing.T) {
	if markdown := RenderVerifiedFindings(nil); !strings.Contains(markdown, "未验证漏洞") {
		t.Fatalf("markdown = %q", markdown)
	}
}

func TestPublishWithReportWritesModelMarkdown(t *testing.T) {
	session := artifactTestSession(t)
	session.Findings = []verify.VerificationResult{{
		Verdict:      verify.VerdictVerified,
		VulnType:     verify.VulnXSS,
		Confidence:   0.95,
		Curl:         "curl -i -X GET 'https://example.test/?q=payload'",
		EvidencePath: "evidence/verification-001.json",
	}}
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
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# PentGo Agent Report", "## 已验证发现", "curl -i -X GET", "evidence/verification-001.json", "# 模型报告"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("report missing %q: %q", want, body)
		}
	}
	if strings.Index(string(body), "## 已验证发现") > strings.Index(string(body), "# 模型报告") {
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
	if err != nil || !strings.Contains(string(body), "## 已验证发现") || !strings.Contains(string(body), "## Execution Timeline") {
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
