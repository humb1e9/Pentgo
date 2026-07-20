package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
	sess "pentgo/internal/runtime/session"
)

func TestServiceUsesFourthModelRequestForFinalReport(t *testing.T) {
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
		{response: agent.Response{Content: "NO_FINDINGS"}},
		{response: agent.Response{Content: "## 执行摘要\n已完成检查。\n"}},
	}}
	service := newTestService(client)
	var events []string
	result, err := service.Run(context.Background(), validRequest(t), func(event Event) { events = append(events, event.Message) })
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(result.Artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 4 || !strings.Contains(string(body), "# PentGo Agent Report") || !strings.Contains(string(body), "## 已验证发现") || !strings.Contains(string(body), "## 执行摘要") {
		t.Fatalf("requests/report = %d/%q", len(client.requests), body)
	}
	if !strings.Contains(client.requests[2].SystemPrompt, "PENTGO FINDING") {
		t.Fatalf("consolidation request = %+v", client.requests[2])
	}
	if !strings.Contains(client.requests[3].Messages[0].Content, "反幻觉审计") {
		t.Fatalf("report request missing audit: %q", client.requests[3].Messages[0].Content)
	}
	if !containsEvent(events, "Generating final report.") || !containsEvent(events, "Final report generated.") {
		t.Fatalf("events = %q", events)
	}
}

func TestServicePublishesTimelineWhenReportCallFails(t *testing.T) {
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
		{response: agent.Response{Content: "NO_FINDINGS"}},
		{err: errors.New("report provider unavailable")},
	}}
	service := newTestService(client)
	var events []string
	result, err := service.Run(context.Background(), validRequest(t), func(event Event) { events = append(events, event.Message) })
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := os.ReadFile(result.Artifacts.Markdown)
	if readErr != nil || !strings.Contains(string(body), "## Execution Timeline") || result.RunError != nil {
		t.Fatalf("body/readErr/runError = %q/%v/%v", body, readErr, result.RunError)
	}
	if len(client.requests) != 4 || !containsEvent(events, "Final report fell back to execution timeline.") {
		t.Fatalf("requests/events = %d/%q", len(client.requests), events)
	}
}

func TestServicePassesFrameworkVerifiedFindingToReport(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		value := request.URL.Query().Get("q")
		_, _ = writer.Write([]byte("<div>" + value + "</div>"))
	}))
	defer target.Close()
	payloadURL := target.URL + "/?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E"
	baselineURL := target.URL + "/?q=benign"
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
		{response: agent.Response{Content: "=== PENTGO FINDING ===\ntype: xss\nmethod: GET\nurl: " + payloadURL + "\nbaseline_url: " + baselineURL + "\npayload: <script>alert(1)</script>\n=== END PENTGO FINDING ==="}},
		{response: agent.Response{Content: "## 执行摘要\n已完成检查。\n"}},
	}}
	service := newTestService(client)
	result, err := service.Run(context.Background(), Request{
		Target:     sess.Target{Raw: target.URL, Canonical: target.URL},
		Intent:     "检查反射输出",
		OutputRoot: t.TempDir(),
	}, nil)
	if err != nil || result.RunError != nil {
		t.Fatalf("result/err = %+v/%v", result, err)
	}
	if len(client.requests) != 4 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	reportContext := client.requests[3].Messages[0].Content
	for _, want := range []string{"框架已验证", "VERDICT: VERIFIED", "curl -i -X GET"} {
		if !strings.Contains(reportContext, want) {
			t.Fatalf("report context missing %q: %q", want, reportContext)
		}
	}
	verificationEvidence, readErr := os.ReadFile(filepath.Join(result.Artifacts.Directory, "evidence", "verification-001.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var record struct {
		PayloadResponseBody  string `json:"payload_response_body"`
		BaselineResponseBody string `json:"baseline_response_body"`
	}
	if err := json.Unmarshal(verificationEvidence, &record); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.PayloadResponseBody, "<script>alert(1)</script>") || !strings.Contains(record.BaselineResponseBody, "benign") {
		t.Fatalf("verification record = %+v", record)
	}
	sessionJSON, readErr := os.ReadFile(result.Artifacts.SessionJSON)
	if readErr != nil || !strings.Contains(string(sessionJSON), `"findings"`) || !strings.Contains(string(sessionJSON), `"evidence_path"`) {
		t.Fatalf("session JSON/readErr = %s/%v", sessionJSON, readErr)
	}
	reportBody, readErr := os.ReadFile(result.Artifacts.Markdown)
	if readErr != nil || !strings.Contains(string(reportBody), "## 已验证发现") || !strings.Contains(string(reportBody), "evidence/verification-001.json") {
		t.Fatalf("report/readErr = %s/%v", reportBody, readErr)
	}
}

func TestServicePublishesAuthenticatedCredentialEvidenceFromLocalServer(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/login" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte("login form"))
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		http.SetCookie(writer, &http.Cookie{Name: "sid", Value: "fixture-cookie", Path: "/"})
		_, _ = writer.Write([]byte("dashboard"))
	}))
	defer target.Close()

	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
		{response: agent.Response{Content: "=== PENTGO FINDING ===\ntype: credential\nlogin_url: " + target.URL + "/login\nlogin_body: username=fixture&password=fixture-secret\nusername: fixture\npayload: username=fixture\n=== END PENTGO FINDING ==="}},
		{response: agent.Response{Content: "## 执行摘要\n本地认证会话已验证。\n"}},
	}}
	service := newTestService(client)
	result, err := service.Run(context.Background(), Request{
		Target:     sess.Target{Raw: target.URL, Canonical: target.URL},
		Intent:     "验证本地认证会话",
		OutputRoot: t.TempDir(),
	}, nil)
	if err != nil || result.RunError != nil || len(result.Session.Findings) != 1 {
		t.Fatalf("result/err = %+v/%v", result, err)
	}

	evidencePath := filepath.Join(result.Artifacts.Directory, "evidence", "verification-001.json")
	evidence, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		LoginVerified    bool     `json:"login_verified"`
		LoginCookieNames []string `json:"login_cookie_names"`
	}
	if err := json.Unmarshal(evidence, &record); err != nil {
		t.Fatal(err)
	}
	if !record.LoginVerified || len(record.LoginCookieNames) != 1 || record.LoginCookieNames[0] != "sid" {
		t.Fatalf("login evidence = %+v", record)
	}
	for _, secret := range []string{"fixture-secret", "fixture-cookie"} {
		if strings.Contains(string(evidence), secret) {
			t.Fatalf("evidence leaked %q: %s", secret, evidence)
		}
	}

	reportBody, err := os.ReadFile(result.Artifacts.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Login verified: true", "Session cookies: sid", "Username: fixture"} {
		if !strings.Contains(string(reportBody), want) {
			t.Fatalf("report missing %q: %s", want, reportBody)
		}
	}
}

func TestServiceSkipsReportCallForCancelledContext(t *testing.T) {
	context, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
	}, onCall: func(call int) {
		if call == 2 {
			cancel()
		}
	}}
	service := newTestService(client)
	var events []string
	result, err := service.Run(context, validRequest(t), func(event Event) { events = append(events, event.Message) })
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 2 || result.Artifacts.Markdown == "" || !containsEvent(events, "Final report fell back to execution timeline.") {
		t.Fatalf("requests/artifacts/events = %d/%+v/%q", len(client.requests), result.Artifacts, events)
	}
}

func TestServiceRejectsEmptyRuntimeTarget(t *testing.T) {
	service := NewService(config.Default(), Dependencies{})
	if _, err := service.Run(context.Background(), Request{Intent: "检查目标"}, nil); err == nil {
		t.Fatal("Run() error = nil")
	}
}

func TestServiceWiresAuthorizationFromConfig(t *testing.T) {
	cfg := config.Default()
	if !cfg.Agent.Authorization.IsEnabled() {
		t.Fatal("precondition: authorization enabled by default")
	}
	cfg.Agent.ExecutionTimeoutSeconds = 1
	responses := []agent.Response{
		{Content: "```python\nimport requests\nrequests.get('https://evil.example.net/x')\n```"},
		{Content: "```python\nimport os\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
		{Content: "NO_FINDINGS"},
		{Content: "## 执行摘要\n已完成检查。\n"},
	}
	dir := t.TempDir()
	service := NewService(cfg, Dependencies{
		NewAgentClient: func(config.AgentConfig) (agent.Client, error) {
			return &sequenceClient{responses: responses}, nil
		},
	})
	result, err := service.Run(context.Background(), Request{
		Target:     sess.Target{Canonical: "https://example.com", Raw: "example.com"},
		Intent:     "检查目标",
		OutputRoot: dir,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Status != sess.SessionDone {
		t.Fatalf("session = %+v", result.Session)
	}
	blocked := false
	for _, event := range result.Session.Timeline {
		if event.Kind == "recovery" && event.Detail == "authorization_blocked" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("expected authorization_blocked event: %+v", result.Session.Timeline)
	}
}

func newTestService(client agent.Client) *Service {
	return NewService(config.Default(), Dependencies{
		Clock:           func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) },
		NewEngagementID: func(time.Time) (string, error) { return "eng-test", nil },
		NewAgentClient:  func(config.AgentConfig) (agent.Client, error) { return client, nil },
	})
}

func validRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Target:     sess.Target{Raw: "https://example.test", Canonical: "https://example.test"},
		Intent:     "检查目标",
		OutputRoot: t.TempDir(),
	}
}

func containsEvent(events []string, fragment string) bool {
	for _, event := range events {
		if strings.Contains(event, fragment) {
			return true
		}
	}
	return false
}

type chatOutcome struct {
	response agent.Response
	err      error
}

type scriptedClient struct {
	outcomes []chatOutcome
	requests []agent.Request
	onCall   func(int)
}

type sequenceClient struct {
	responses []agent.Response
	index     int
}

func (client *sequenceClient) Chat(_ context.Context, _ agent.Request) (agent.Response, error) {
	if client.index >= len(client.responses) {
		return agent.Response{Content: "TASK_COMPLETE"}, nil
	}
	response := client.responses[client.index]
	client.index++
	return response, nil
}

func (client *scriptedClient) Chat(_ context.Context, request agent.Request) (agent.Response, error) {
	client.requests = append(client.requests, request)
	if client.onCall != nil {
		client.onCall(len(client.requests))
	}
	outcome := client.outcomes[0]
	client.outcomes = client.outcomes[1:]
	return outcome.response, outcome.err
}
