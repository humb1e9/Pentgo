package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"pentgo/internal/agent"
	"pentgo/internal/config"
)

func TestOpenCurrentProjectReturnsApplicationError(t *testing.T) {
	coordinator := New(config.Default(), t.TempDir(), Dependencies{})
	if _, err := coordinator.OpenCurrentProject(context.Background()); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenOrCreateWorkspaceUsesHiddenDirectory(t *testing.T) {
	root := t.TempDir()
	coordinator := New(config.Default(), root, Dependencies{})
	project, created, err := coordinator.OpenOrCreateWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !created || project.Name != filepath.Base(root) {
		t.Fatalf("project/created = %#v/%v", project, created)
	}
	if _, err := os.Stat(filepath.Join(root, ".pentgo", "pentgo.db")); err != nil {
		t.Fatal(err)
	}
	if reopened, created, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil || created || reopened.ID != project.ID {
		t.Fatalf("reopened/created/err = %#v/%v/%v", reopened, created, err)
	}
}

func TestNewSessionCreatesDistinctSessions(t *testing.T) {
	coordinator := New(config.Default(), t.TempDir(), Dependencies{})
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.NewSession("交互会话")
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.NewSession("交互会话")
	if err != nil || second.ID == first.ID {
		t.Fatalf("second/err = %#v/%v", second, err)
	}
}

func TestResumeAndDeleteSession(t *testing.T) {
	coordinator := New(config.Default(), t.TempDir(), Dependencies{})
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := coordinator.ResumeSession(session.ID); err != nil || resumed.ID != session.ID {
		t.Fatalf("resumed/err = %#v/%v", resumed, err)
	}
	if err := coordinator.DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ResumeSession(session.ID); err == nil {
		t.Fatal("resume deleted session succeeded")
	}
}

func TestRenameSessionPersistsName(t *testing.T) {
	root := t.TempDir()
	coordinator := New(config.Default(), root, Dependencies{})
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("new")
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.RenameSession(session.ID, "API reconnaissance"); err != nil {
		t.Fatal(err)
	}
	if renamed, err := coordinator.ResumeSession(session.ID); err != nil || renamed.Name != "API reconnaissance" {
		t.Fatalf("renamed/err = %#v/%v", renamed, err)
	}
	if err := coordinator.CloseProject(); err != nil {
		t.Fatal(err)
	}
	reopened := New(config.Default(), root, Dependencies{})
	defer reopened.CloseProject()
	if _, err := reopened.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if renamed, err := reopened.ResumeSession(session.ID); err != nil || renamed.Name != "API reconnaissance" {
		t.Fatalf("reopened/err = %#v/%v", renamed, err)
	}
}

func TestCoordinatorMessagesReturnsTranscript(t *testing.T) {
	fixture := &coordinatorModel{messages: []*schema.Message{schema.AssistantMessage("完成", nil)}}
	coordinator := New(config.Default(), t.TempDir(), Dependencies{NewModel: func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) {
		return fixture, nil
	}})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-coordinator.Submit(context.Background(), session.ID, "检查 TARGET"); err != nil {
		t.Fatal(err)
	}
	messages := coordinator.Messages(session.ID)
	if len(messages) != 2 || messages[0].Role != agent.RoleUser || messages[1].Role != agent.RoleAssistant {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestCoordinatorRegistersFilesystemToolsForEachAgent(t *testing.T) {
	root := t.TempDir()
	fixture := &coordinatorModel{messages: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "call-1", Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file_path":"notes/result.txt","content":"recorded"}`}}}),
		schema.AssistantMessage("完成", nil),
	}}
	coordinator := New(config.Default(), root, Dependencies{NewModel: func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) {
		return fixture, nil
	}})
	defer coordinator.CloseProject()
	if _, _, err := coordinator.OpenOrCreateWorkspace(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("filesystem")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-coordinator.Submit(context.Background(), session.ID, "写入文件"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "notes", "result.txt")); err != nil || string(content) != "recorded" {
		t.Fatalf("content/err = %q/%v", content, err)
	}
	messages := coordinator.Messages(session.ID)
	if len(messages) < 3 || messages[2].Role != agent.RoleTool || !strings.Contains(messages[2].Content, "evidence_ref: 1") {
		t.Fatalf("messages = %#v", messages)
	}
}

type coordinatorModel struct {
	messages []*schema.Message
	inputs   [][]*schema.Message
}

func (fixture *coordinatorModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	fixture.inputs = append(fixture.inputs, append([]*schema.Message(nil), input...))
	if len(fixture.messages) == 0 {
		return nil, fmt.Errorf("model fixture exhausted")
	}
	message := fixture.messages[0]
	fixture.messages = fixture.messages[1:]
	return message, nil
}
func (*coordinatorModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported")
}
func (fixture *coordinatorModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return fixture, nil
}

func TestCoordinatorUsesRuntimeAndTranscriptReplay(t *testing.T) {
	root := t.TempDir()
	fixture := &coordinatorModel{messages: []*schema.Message{schema.AssistantMessage("第一轮", nil), schema.AssistantMessage("第二轮", nil), schema.AssistantMessage("重开后", nil)}}
	deps := Dependencies{NewModel: func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error) { return fixture, nil }}
	coordinator := New(config.Default(), root, deps)
	project, err := coordinator.CreateProject(context.Background(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	session, err := coordinator.NewSession("检查 https://fixture.local")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"第一条请求", "第二条请求"} {
		if err := <-coordinator.Submit(context.Background(), session.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	current, open := coordinator.CurrentProject()
	if !open || current.ID != project.ID {
		t.Fatalf("project=%#v open=%v", current, open)
	}
	snapshot := coordinator.Sessions()[0]
	if snapshot.Turns != 2 || snapshot.FinalSummary != "第二轮" {
		t.Fatalf("session=%#v", snapshot)
	}
	if err := coordinator.CloseProject(); err != nil {
		t.Fatal(err)
	}

	reopened := New(config.Default(), filepath.Join(root, project.ID), deps)
	if _, err := reopened.OpenCurrentProject(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-reopened.Submit(context.Background(), session.ID, "重开后的请求"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.inputs) != 3 || !hasEinoContent(fixture.inputs[2], "第一条请求") || !hasEinoContent(fixture.inputs[2], "第二轮") || !hasEinoContent(fixture.inputs[2], "重开后的请求") {
		t.Fatalf("replayed inputs=%#v", fixture.inputs)
	}
	if err := reopened.CloseProject(); err != nil {
		t.Fatal(err)
	}
}

func hasEinoContent(messages []*schema.Message, expected string) bool {
	for _, message := range messages {
		if message.Content == expected {
			return true
		}
	}
	return false
}
