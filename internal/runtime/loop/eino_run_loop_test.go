package loop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/runtime/authz"
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

// TestRunEinoRecoversFromRefusalThenProceeds is the openai-path analogue of the
// text-path TestRunnerRecoversFromRefusalThenProceeds. A refusal turn produces
// no tool call, so the react graph routes to END and the Run finishes; the
// outer re-drive loop must re-prompt with the authorization nudge so the model
// (next scripted turn) runs execute_code and then completes. Driving this
// through the REAL adk graph is the point: a direct unit test on the counter
// would pass even though the single-Run graph never re-enters the model, which
// is exactly the "green tests != real usability" trap.
func TestRunEinoRecoversFromRefusalThenProceeds(t *testing.T) {
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "probe evidence\n",
	}}}
	fake := &scriptedToolModel{turns: []*schema.Message{
		schema.AssistantMessage("I can't help with that.", nil),
		toolCallMessage("Proceeding with an authorized probe.", "execute_code", `{"language":"python","code":"import urllib.request\nprint(urllib.request.urlopen('https://example.com').status)"}`),
		exitMessage("done: recovered after refusal"),
	}}
	runner := NewRunner(nil, executor, einoTestConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.Status != sess.SessionDone || session.StopReason != "task_complete" {
		t.Fatalf("status/stop = %q/%q; want done/task_complete (refusal recovered then completed)", session.Status, session.StopReason)
	}
	if len(executor.inputs) != 1 {
		t.Fatalf("expected exactly one execute_code after refusal recovery, got %d", len(executor.inputs))
	}
}

// TestRunEinoNoCodeLimitCompletes proves the no-tool-call counter accumulates
// across re-drives and terminates as no_executable_response (not max_turns).
// The model never calls a tool; each text-only turn re-drives until NoCodeLimit.
func TestRunEinoNoCodeLimitCompletes(t *testing.T) {
	executor := &recordingExecutor{}
	// More text-only turns than NoCodeLimit; the loop must stop at the limit.
	turns := make([]*schema.Message, 0, 6)
	for i := 0; i < 6; i++ {
		turns = append(turns, schema.AssistantMessage("Here is my prose analysis without running anything.", nil))
	}
	fake := &scriptedToolModel{turns: turns}
	config := einoTestConfig() // NoCodeLimit: 3
	runner := NewRunner(nil, executor, config, nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.StopReason != "no_executable_response" {
		t.Fatalf("stop = %q; want no_executable_response at NoCodeLimit", session.StopReason)
	}
	if session.Turn != config.NoCodeLimit {
		t.Fatalf("turns = %d; want NoCodeLimit=%d (counter must accumulate across re-drives)", session.Turn, config.NoCodeLimit)
	}
	if len(executor.inputs) != 0 {
		t.Fatalf("no code should have executed, got %d", len(executor.inputs))
	}
}

// TestRunEinoStuckFailsOnRepeatedToolCalls proves stuck detection works across
// turns on the openai path: an identical execute_code call repeated每turn keeps
// identical execute_code call repeated each turn keeps the react graph alive
// (tool calls re-enter the model) yet the fingerprint counter reaches
// HardStuckTurns and fails the session as stuck.
func TestRunEinoStuckFailsOnRepeatedToolCalls(t *testing.T) {
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "same output\n",
	}}}
	same := toolCallMessage("", "execute_code", `{"language":"python","code":"print('identical probe')"}`)
	turns := make([]*schema.Message, 0, 8)
	for i := 0; i < 8; i++ {
		turns = append(turns, same)
	}
	fake := &scriptedToolModel{turns: turns}
	config := einoTestConfig() // HardStuckTurns: 5
	runner := NewRunner(nil, executor, config, nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, fake); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.Status != sess.SessionFailed || session.StopReason != "stuck" {
		t.Fatalf("status/stop = %q/%q; want failed/stuck after %d identical turns", session.Status, session.StopReason, config.HardStuckTurns)
	}
	if session.Turn != config.HardStuckTurns {
		t.Fatalf("turns = %d; want HardStuckTurns=%d", session.Turn, config.HardStuckTurns)
	}
}

// TestRunEinoConsolidateAndVerifyIsDeterministic locks the framework-owned
// verification seam on the openai path: RunEino (eino model) gathers evidence,
// then ConsolidateAndVerify runs off the RunEino-built history through the
// openai TEXT client and the REAL HTTPVerifier against a local httptest server.
// The verdict must be framework-decided (VerdictVerified for a genuinely
// reflected payload), never model self-certified — and identical across runs.
func TestRunEinoConsolidateAndVerifyIsDeterministic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Reflect the query verbatim so the framework verifier — not the model —
		// observes the payload in the response body.
		_, _ = io.WriteString(writer, "<div>"+request.URL.Query().Get("q")+"</div>")
	}))
	defer server.Close()

	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "reflected payload observed\n",
	}}}
	// The eino model drives the engagement (evidence gathering + completion).
	eino := &scriptedToolModel{turns: []*schema.Message{
		toolCallMessage("Probing reflection.", "execute_code", `{"language":"python","code":"import urllib.request\nprint(urllib.request.urlopen('`+server.URL+`/?q=probe').read())"}`),
		exitMessage("reflected XSS candidate identified"),
	}}
	// The text client backs ONLY consolidation: it emits one PENTGO FINDING block
	// pointing at the reflecting endpoint. The verifier decides the verdict.
	finding := "=== PENTGO FINDING ===\n" +
		"type: xss\nmethod: GET\n" +
		"url: " + server.URL + "/?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E\n" +
		"baseline_url: " + server.URL + "/?q=benign\n" +
		"payload: <script>alert(1)</script>\n" +
		"=== END PENTGO FINDING ==="
	textClient := &scriptedClient{responses: []agent.Response{{Content: finding}}}

	config := einoTestConfig()
	config.MaxFindings = 5
	config.Verifier = verify.NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	runner := NewRunner(textClient, executor, config, nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: server.URL}, "check reflection", time.Now().UTC())

	if err := runner.RunEino(context.Background(), session, eino); err != nil {
		t.Fatalf("RunEino error = %v", err)
	}
	if session.Status != sess.SessionDone {
		t.Fatalf("run did not complete: %q/%q", session.Status, session.StopReason)
	}
	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 verified finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].Verdict != verify.VerdictVerified {
		t.Fatalf("verdict = %s (checks failed: %v); framework must VERIFY a reflected payload", findings[0].Verdict, findings[0].ChecksFailed)
	}
}
