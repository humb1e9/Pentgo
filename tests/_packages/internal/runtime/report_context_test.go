package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
)

func TestReportContextPromptExcludesCodeAndBoundsOutput(t *testing.T) {
	reportContext := ReportContext{
		Target: "https://example.test",
		Intent: "检查首页",
		Turns: []ReportTurn{{
			Number:   1,
			Decision: "检查首页响应",
			Blocks: []ReportBlock{{
				Index:        1,
				Language:     LanguagePython,
				Status:       ExecutionSucceeded,
				Stdout:       strings.Repeat("测", 9000),
				EvidencePath: "evidence/agent-turn-001-block-001.json",
			}},
		}},
	}

	prompt := reportContext.PromptText()
	if len(prompt) > maxReportContextBytes {
		t.Fatalf("prompt length = %d", len(prompt))
	}
	if strings.Contains(prompt, "print(") || !strings.Contains(prompt, "evidence/agent-turn-001-block-001.json") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestReportContextRendersDeclaredLabels(t *testing.T) {
	reportContext := ReportContext{
		Target: "https://example.com",
		Turns: []ReportTurn{{
			Number:         1,
			Decision:       "发现未授权接口",
			DeclaredLabels: []EvidenceLevel{EvidenceLikely},
			Blocks: []ReportBlock{{
				Index: 1,
				Level: EvidenceVerified,
			}},
		}},
	}
	text := reportContext.PromptText()
	if !strings.Contains(text, "模型声明标签: LIKELY") || !strings.Contains(text, "执行等级: VERIFIED") {
		t.Fatalf("prompt missing separated labels: %s", text)
	}
}

func TestReportContextRendersFrameworkFindingsBeforeLongTurnHistory(t *testing.T) {
	reportContext := ReportContext{
		Target: "https://example.test",
		VerifiedFindings: []VerificationResult{{
			Verdict:    VerdictVerified,
			VulnType:   VulnXSS,
			Confidence: 0.95,
			Summary:    "xss VERIFIED confidence=0.95",
			Curl:       "curl -i -X GET 'https://example.test/?q=%3Cscript%3E'",
		}},
	}
	for number := 1; number <= 300; number++ {
		reportContext.Turns = append(reportContext.Turns, ReportTurn{
			Number:   number,
			Decision: strings.Repeat("execution summary ", 80),
		})
	}

	text := reportContext.PromptText()
	for _, want := range []string{"框架验证发现", "框架已验证", "VERDICT: VERIFIED", "confidence: 0.95", "curl -i -X GET"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PromptText missing %q: %q", want, text)
		}
	}
	if len(text) > maxReportContextBytes {
		t.Fatalf("PromptText len = %d", len(text))
	}
}

func TestRunnerCopiesFrameworkFindingsIntoReportContext(t *testing.T) {
	runner := NewRunner(&scriptedClient{}, &recordingExecutor{}, defaultRunnerConfig(), nil, nil)
	runner.findings = []VerificationResult{{
		Verdict:      VerdictLikely,
		VulnType:     VulnSQLI,
		ChecksPassed: []string{"P2 causal difference"},
		ChecksFailed: []string{"P1 reproduction count below 3"},
	}}
	session := NewSession(Target{Canonical: "https://example.test"}, "检查首页", time.Now().UTC())

	reportContext := runner.ReportContext(session)
	if len(reportContext.VerifiedFindings) != 1 || reportContext.VerifiedFindings[0].Verdict != VerdictLikely {
		t.Fatalf("framework findings = %+v", reportContext.VerifiedFindings)
	}
	runner.findings[0].ChecksPassed[0] = "mutated"
	if reportContext.VerifiedFindings[0].ChecksPassed[0] != "P2 causal difference" {
		t.Fatalf("report context aliases runner findings: %+v", reportContext.VerifiedFindings)
	}
}

func TestRunnerExposesCodeFreeExecutionReportContext(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "[LIKELY] 检查首页。\n```python\nimport os\nprint(os.environ['PENTGO_TARGET'])\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block:        CodeBlock{Index: 1, Language: LanguagePython},
		Status:       ExecutionSucceeded,
		ExitCode:     0,
		Stdout:       "HTTP 200\n",
		EvidencePath: "evidence/agent-turn-001-block-001.json",
	}}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.test"}, "检查首页", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	reportContext := runner.ReportContext(session)
	prompt := reportContext.PromptText()
	if !strings.Contains(prompt, "检查首页。") || !strings.Contains(prompt, "LIKELY") || !strings.Contains(prompt, "HTTP 200") || !strings.Contains(prompt, "evidence/agent-turn-001-block-001.json") {
		t.Fatalf("prompt = %q", prompt)
	}
	if strings.Contains(prompt, "import os") || strings.Contains(prompt, "print(os.environ") {
		t.Fatalf("prompt includes model code: %q", prompt)
	}
}
