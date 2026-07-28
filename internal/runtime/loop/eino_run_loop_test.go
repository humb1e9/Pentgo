package loop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"pentgo/internal/runtime/exec"
	sess "pentgo/internal/runtime/session"
	"pentgo/internal/runtime/verify"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// scriptedToolModel is a fake model.ToolCallingChatModel that replays a fixed
// sequence of assistant messages, one per Generate call. It lets the ADK react
// loop run end-to-end without a network model: tool-call turns drive the
// execute_code/declare_session tools, and a final exit tool-call ends the run.
type scriptedToolModel struct {
	turns     []*schema.Message
	generated int
}

func (m *scriptedToolModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.generated >= len(m.turns) {
		// Exhausted script: keep returning a bare assistant message so the loop
		// terminates via MaxIterations rather than panicking.
		return schema.AssistantMessage("(no further action)", nil), nil
	}
	message := m.turns[m.generated]
	m.generated++
	return message, nil
}

func (m *scriptedToolModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("streaming unsupported in test model")
}

func (m *scriptedToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// toolCallMessage builds an assistant message carrying a single tool call.
func toolCallMessage(content, toolName, argsJSON string) *schema.Message {
	return schema.AssistantMessage(content, []schema.ToolCall{
		{
			ID:       toolName + "-call",
			Function: schema.FunctionCall{Name: toolName, Arguments: argsJSON},
		},
	})
}

func exitMessage(finalResult string) *schema.Message {
	return toolCallMessage("", "complete_task", fmt.Sprintf(`{"final_result":%q}`, finalResult))
}

func einoTestConfig() RunnerConfig {
	return RunnerConfig{MaxTurns: 12, NoCodeLimit: 3, NetworkBackoff: time.Millisecond, SoftStuckTurns: 3, HardStuckTurns: 5, RefusalLimit: 3}
}

func TestRunEinoExecutesCodeThenExits(t *testing.T) {
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "probe evidence\n",
	}}}
	fake := &scriptedToolModel{turns: []*schema.Message{
		toolCallMessage("Running a probe.", "execute_code", `{"language":"python","code":"import urllib.request\nr = urllib.request.urlopen('https://example.com')\nprint(r.status)"}`),
		exitMessage("done: probe returned evidence"),
	}}
	runner := NewRunner(nil, executor, einoTestConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.Status != sess.SessionDone {
		t.Fatalf("session status = %q (%+v)", session.Status, session.StopReason)
	}
	// Lock the clean Action.Exit path: complete_task must terminate via the exit
	// action ("task_complete"), not the NoCodeLimit fallback
	// ("no_executable_response") or a leak ("max_turns").
	if session.StopReason != "task_complete" {
		t.Fatalf("stop reason = %q; want task_complete (clean complete_task exit)", session.StopReason)
	}
	if len(executor.inputs) != 1 {
		t.Fatalf("execute_code should run exactly once, got %d", len(executor.inputs))
	}
	if len(executor.inputs[0].Blocks) != 1 || executor.inputs[0].Blocks[0].Block.Language != exec.LanguagePython {
		t.Fatalf("unexpected execution input: %+v", executor.inputs[0])
	}
	// Report accumulators must be populated for ReportContext / consolidation.
	report := runner.ReportContext(session)
	if len(report.Turns) == 0 {
		t.Fatalf("expected report turns, got none: %+v", report)
	}
	foundBlock := false
	for _, turn := range report.Turns {
		if len(turn.Blocks) > 0 {
			foundBlock = true
		}
	}
	if !foundBlock {
		t.Fatalf("expected at least one recorded report block: %+v", report.Turns)
	}
}

func TestRunEinoEvidenceGateBlocksPrematureExit(t *testing.T) {
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "late evidence\n",
	}}}
	// Turn 1 tries to exit with no prior evidence -> gate returns soft error.
	// Turn 2 runs code (evidence). Turn 3 exits successfully.
	fake := &scriptedToolModel{turns: []*schema.Message{
		exitMessage("premature: nothing tested yet"),
		toolCallMessage("Gathering evidence now.", "execute_code", `{"language":"python","code":"import urllib.request\nr = urllib.request.urlopen('https://example.com')\nprint(r.status)"}`),
		exitMessage("done with evidence"),
	}}
	runner := NewRunner(nil, executor, einoTestConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.Status != sess.SessionDone {
		t.Fatalf("session status = %q; premature exit should be gated then completed after evidence", session.Status)
	}
	// The premature complete_task (turn 1) must be rerouted to evidence_gate and
	// loop back; the real completion (turn 3, after evidence) must be the clean
	// exit action. If turn 1 had leaked through, stop reason would not be
	// task_complete and execute_code would not have run exactly once.
	if session.StopReason != "task_complete" {
		t.Fatalf("stop reason = %q; want task_complete after gated evidence", session.StopReason)
	}
	if len(executor.inputs) != 1 {
		t.Fatalf("expected exactly one execute_code after gate, got %d", len(executor.inputs))
	}
}

func TestRunEinoRedactsSessionSecretsInToolOutput(t *testing.T) {
	// A declare_session establishes a verified identity whose cookie is injected
	// into execute_code's child env. The executor echoes that value into stdout;
	// RunEino must redact it before it reaches history or report blocks.
	secret := "SUPERSECRETVALUE123"
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "leaked cookie: sid=" + secret + "\n",
	}}}
	verifier := &recordingSessionVerifier{loginResults: []verify.LoginResult{{
		Attempted:           true,
		Verified:            true,
		StatusCode:          200,
		CookieNames:         []string{"sid"},
		MeaningfulCookie:    true,
		SessionCookieHeader: "sid=" + secret,
	}}}
	config := einoTestConfig()
	config.Verifier = verifier
	fake := &scriptedToolModel{turns: []*schema.Message{
		toolCallMessage("login", "declare_session", `{"name":"user_a","role":"user","username":"alice","login_url":"https://example.com/login","login_method":"POST","login_body":"username=alice&password=x","login_content_type":"application/x-www-form-urlencoded"}`),
		toolCallMessage("echo cookie", "execute_code", `{"language":"python","code":"import os\ncookie = os.environ.get('PENTGO_SESSION_user_a_COOKIE','')\nprint('cookie:', cookie)"}`),
		exitMessage("done"),
	}}
	runner := NewRunner(nil, executor, config, nil, func(context.Context, time.Duration) error { return nil })

	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if len(verifier.loginSpecs) != 1 {
		t.Fatalf("declare_session should invoke the framework login verifier once, got %d", len(verifier.loginSpecs))
	}
	// The cookie must have been injected into the child env.
	if len(executor.inputs) == 0 || executor.inputs[len(executor.inputs)-1].ExtraEnv["PENTGO_SESSION_user_a_COOKIE"] == "" {
		t.Fatalf("expected session cookie injected into ExtraEnv: %+v", executor.inputs)
	}
	report := runner.ReportContext(session)
	for _, turn := range report.Turns {
		for _, block := range turn.Blocks {
			if strings.Contains(block.Stdout, secret) {
				t.Fatalf("secret leaked into report block stdout: %q", block.Stdout)
			}
		}
	}
}
