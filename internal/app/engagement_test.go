package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pentgo/internal/config"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fixtureModel struct{ message *schema.Message }

func (m *fixtureModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return m.message, nil
}
func (m *fixtureModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported")
}
func (m *fixtureModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestServicePublishesDirectNaturalCompletion(t *testing.T) {
	fixture := &fixtureModel{message: schema.AssistantMessage("No findings.", nil)}
	service := NewService(config.Default(), Dependencies{
		NewEngagementID: func(time.Time) (string, error) { return "eng-test", nil },
		NewEinoModel:    func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) { return fixture, nil },
	})
	result, err := service.Run(context.Background(), Request{Target: sess.Target{Canonical: "https://fixture.local"}, Intent: "TASK", OutputRoot: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunError != nil || result.Session.Status != sess.SessionDone || result.Session.FinalSummary != "No findings." {
		t.Fatalf("result = %+v", result)
	}
	for _, path := range []string{result.Artifacts.EvidenceJSONL, result.Artifacts.SessionJSON, result.Artifacts.Markdown, result.Artifacts.WorkDirectory} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(result.Artifacts.EvidenceJSONL)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("evidence = %q", data)
	}
	if filepath.Base(result.Artifacts.Directory) != "eng-test" {
		t.Fatalf("artifacts = %+v", result.Artifacts)
	}
}
