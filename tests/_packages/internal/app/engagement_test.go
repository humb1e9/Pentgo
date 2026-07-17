package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/runtime"
)

func TestServiceUsesThirdModelRequestForFinalReport(t *testing.T) {
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
		{response: agent.Response{Content: "# 最终报告\n\n## 已验证发现\n未验证漏洞。\n"}},
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
	if len(client.requests) != 3 || string(body) != "# 最终报告\n\n## 已验证发现\n未验证漏洞。\n" {
		t.Fatalf("requests/report = %d/%q", len(client.requests), body)
	}
	if !containsEvent(events, "Generating final report.") || !containsEvent(events, "Final report generated.") {
		t.Fatalf("events = %q", events)
	}
}

func TestServicePublishesTimelineWhenReportCallFails(t *testing.T) {
	client := &scriptedClient{outcomes: []chatOutcome{
		{response: agent.Response{Content: "```python\nimport os\nprint('evidence')\n```"}},
		{response: agent.Response{Content: "TASK_COMPLETE"}},
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
	if len(client.requests) != 3 || !containsEvent(events, "Final report fell back to execution timeline.") {
		t.Fatalf("requests/events = %d/%q", len(client.requests), events)
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
		Target:     runtime.Target{Raw: "https://example.test", Canonical: "https://example.test"},
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

func (client *scriptedClient) Chat(_ context.Context, request agent.Request) (agent.Response, error) {
	client.requests = append(client.requests, request)
	if client.onCall != nil {
		client.onCall(len(client.requests))
	}
	outcome := client.outcomes[0]
	client.outcomes = client.outcomes[1:]
	return outcome.response, outcome.err
}
