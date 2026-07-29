package loop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pentgo/internal/runtime/evidence"
	"pentgo/internal/runtime/exec"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type scriptedToolModel struct {
	turns     []*schema.Message
	generated int
}

func (m *scriptedToolModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.generated >= len(m.turns) {
		return schema.AssistantMessage("", nil), nil
	}
	message := m.turns[m.generated]
	m.generated++
	return message, nil
}
func (m *scriptedToolModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported")
}
func (m *scriptedToolModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func toolCallMessage(name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: name + "-call", Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

type blockExecutorFunc func(context.Context, exec.ExecutionInput) []exec.ExecutionResult

func (f blockExecutorFunc) Execute(ctx context.Context, input exec.ExecutionInput) []exec.ExecutionResult {
	return f(ctx, input)
}
func testJournal(t *testing.T) *evidence.Journal {
	t.Helper()
	journal, err := evidence.NewJournal(filepath.Join(t.TempDir(), "evidence.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	return journal
}
func runningSession(t *testing.T) *sess.AgentSession {
	t.Helper()
	session := sess.NewSession(sess.Target{Canonical: "https://fixture.local"}, "TASK", time.Now().UTC())
	if err := session.Start(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return session
}
func successfulExecutor() BlockExecutor {
	return blockExecutorFunc(func(_ context.Context, input exec.ExecutionInput) []exec.ExecutionResult {
		now := time.Now().UTC()
		return []exec.ExecutionResult{{Block: input.Blocks[0].Block, Status: exec.ExecutionSucceeded, ExitCode: 0, Stdout: "RESULT\n", StartedAt: now, FinishedAt: now}}
	})
}
func testConfig() RunnerConfig { return RunnerConfig{MaxTurns: 10, AllowPrivateHosts: true} }

func TestRunEinoRecordsFindingAndNaturalFinalResponse(t *testing.T) {
	journal, session := testJournal(t), runningSession(t)
	runner := NewRunner(successfulExecutor(), journal, testConfig(), nil, func(context.Context, time.Duration) error { return nil })
	model := &scriptedToolModel{turns: []*schema.Message{toolCallMessage("execute_python", `{"script":"import os\nos.getenv('PENTGO_TARGET')"}`), toolCallMessage("record_finding", `{"title":"Fixture finding","severity":"high","description":"Observed RESULT.","evidence_refs":[1],"recommendation":"Apply fixture control."}`), schema.AssistantMessage("Completed fixture task.", nil)}}
	if err := runner.RunEino(context.Background(), session, model); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone || session.StopReason != "agent_finished" || session.FinalSummary != "Completed fixture task." || len(session.Findings) != 1 {
		t.Fatalf("session = %+v", session)
	}
	record, ok := journal.Lookup(1)
	if !ok || !record.Success || record.Tool != "execute_python" || !strings.Contains(record.Output, "[evidence_ref: 1]") {
		t.Fatalf("record = %+v, %t", record, ok)
	}
}
func TestRunEinoAllowsDirectNaturalFinalResponse(t *testing.T) {
	journal, session := testJournal(t), runningSession(t)
	err := NewRunner(successfulExecutor(), journal, testConfig(), nil, nil).RunEino(context.Background(), session, &scriptedToolModel{turns: []*schema.Message{schema.AssistantMessage("No findings.", nil)}})
	if err != nil || session.Status != sess.SessionDone || session.FinalSummary != "No findings." {
		t.Fatalf("session = %+v, err = %v", session, err)
	}
}
func TestRecordFindingRejectsFailedReference(t *testing.T) {
	journal := testJournal(t)
	if _, err := journal.Record("exec", map[string]any{}, false, "failed", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	tools := &einoToolSet{journal: journal, session: runningSession(t), loaded: map[string]bool{}}
	got, err := tools.recordFinding(context.Background(), recordFindingArgs{Title: "T", Severity: "high", Description: "D", EvidenceRefs: []int{1}, Recommendation: "R"})
	if err != nil || !strings.Contains(got, "not successful") {
		t.Fatalf("result = %q, err = %v", got, err)
	}
}
