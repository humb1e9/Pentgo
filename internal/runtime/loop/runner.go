package loop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/exec"
	sess "pentgo/internal/runtime/session"
	"pentgo/internal/runtime/verify"
	"pentgo/skills"
)

var skillLoadPattern = regexp.MustCompile(`(?m)^SKILL_LOAD:\s*([a-z][a-z0-9_-]*)\s*$`)

// BlockExecutor 是 Runner 所依赖的本地代码执行边界。
type BlockExecutor interface {
	Execute(context.Context, exec.ExecutionInput) []exec.ExecutionResult
}

// SkillLoader 读取一个已注册的只读 Skill。
type SkillLoader func(string) (string, error)

// Sleeper 使恢复等待可被测试替换和任务取消中断。
type Sleeper func(context.Context, time.Duration) error

// FindingVerifier performs framework-owned verification for a model declaration.
type FindingVerifier interface {
	VerifyWithEvidence(context.Context, verify.FindingSpec) (verify.VerificationResult, verify.VerificationRecord)
}

type sessionEstablisher interface {
	EstablishSession(context.Context, verify.LoginSpec) verify.LoginResult
}

type optionsFindingVerifier interface {
	VerifyWithEvidenceOptions(context.Context, verify.FindingSpec, verify.VerifyOptions) (verify.VerificationResult, verify.VerificationRecord)
}

// RunnerConfig 约束模型循环和恢复策略。
type RunnerConfig struct {
	MaxTurns           int
	MaxFindings        int
	NoCodeLimit        int
	MaxBlocksPerTurn   int
	ProviderRetryDelay time.Duration
	NetworkBackoff     time.Duration
	SoftStuckTurns     int
	HardStuckTurns     int
	RefusalLimit       int
	OnEvent            func(RunnerEvent)
	SkillCatalog       []skills.Skill
	Authorizer         *authz.Authorizer
	AllowedHosts       []string
	AllowPrivateHosts  bool
	Verifier           FindingVerifier
	EvidenceSink       exec.EvidenceSink
}

// RunnerEvent 是可安全显示在终端中的运行时摘要，不包含模型代码和原始输出。
type RunnerEvent struct {
	Turn       int
	Kind       string
	BlockIndex int
	Detail     string
}

// Runner 将模型文本、代码执行和回灌历史串成单一 engagement 循环。
type Runner struct {
	client       agent.Client
	executor     BlockExecutor
	config       RunnerConfig
	load         SkillLoader
	sleep        Sleeper
	history      *History
	reportTurns  []ReportTurn
	findings     []verify.VerificationResult
	findingSpecs []verify.FindingSpec
	catalog      []skills.Skill
	sessionPool  *sess.SessionPool
}

type verificationEvidence struct {
	SchemaVersion          string            `json:"schema_version"`
	VulnType               verify.VulnType   `json:"vuln_type"`
	Verdict                verify.Verdict    `json:"verdict"`
	Confidence             float64           `json:"confidence"`
	Method                 string            `json:"method"`
	PayloadURL             string            `json:"payload_url"`
	BaselineURL            string            `json:"baseline_url,omitempty"`
	RequestHeaders         map[string]string `json:"request_headers,omitempty"`
	RequestBody            string            `json:"request_body,omitempty"`
	BaselineRequestBody    string            `json:"baseline_request_body,omitempty"`
	PayloadStatus          int               `json:"payload_status,omitempty"`
	PayloadResponseBody    string            `json:"payload_response_body,omitempty"`
	PayloadLocation        string            `json:"payload_location,omitempty"`
	BaselineStatus         int               `json:"baseline_status,omitempty"`
	BaselineResponseBody   string            `json:"baseline_response_body,omitempty"`
	Reproductions          int               `json:"reproductions,omitempty"`
	ScopeRejected          bool              `json:"scope_rejected,omitempty"`
	LoginAttempted         bool              `json:"login_attempted"`
	LoginVerified          bool              `json:"login_verified"`
	LoginStatus            int               `json:"login_status,omitempty"`
	LoginCookieNames       []string          `json:"login_cookie_names,omitempty"`
	LoginMeaningfulCookie  bool              `json:"login_meaningful_cookie"`
	LoginSnippet           string            `json:"login_snippet,omitempty"`
	LoginBAttempted        bool              `json:"login_b_attempted"`
	LoginBVerified         bool              `json:"login_b_verified"`
	LoginBStatus           int               `json:"login_b_status,omitempty"`
	LoginBCookieNames      []string          `json:"login_b_cookie_names,omitempty"`
	LoginBMeaningfulCookie bool              `json:"login_b_meaningful_cookie"`
	LoginBSnippet          string            `json:"login_b_snippet,omitempty"`
	IDORDiffReason         string            `json:"idor_diff_reason,omitempty"`
	ChecksPassed           []string          `json:"checks_passed,omitempty"`
	ChecksFailed           []string          `json:"checks_failed,omitempty"`
	Curl                   string            `json:"curl,omitempty"`
}

// NewRunner 创建一个模型循环。nil loader 和 sleeper 使用默认实现。
func NewRunner(client agent.Client, executor BlockExecutor, config RunnerConfig, load SkillLoader, sleep Sleeper) *Runner {
	if config.MaxFindings <= 0 {
		config.MaxFindings = 10
	}
	if config.NoCodeLimit <= 0 {
		config.NoCodeLimit = 3
	}
	if config.ProviderRetryDelay <= 0 {
		config.ProviderRetryDelay = 3 * time.Second
	}
	if config.NetworkBackoff <= 0 {
		config.NetworkBackoff = 15 * time.Second
	}
	if config.SoftStuckTurns <= 0 {
		config.SoftStuckTurns = 3
	}
	if config.HardStuckTurns <= 0 {
		config.HardStuckTurns = 5
	}
	if config.RefusalLimit <= 0 {
		config.RefusalLimit = 3
	}
	if load == nil {
		load = skills.Load
	}
	if sleep == nil {
		sleep = sleepContext
	}
	catalog := config.SkillCatalog
	if catalog == nil {
		catalog = skills.Catalog()
	}
	return &Runner{client: client, executor: executor, config: config, load: load, sleep: sleep, catalog: catalog}
}

// Run 持续请求模型、执行全部代码块并把实际结果回灌，直到会话到达终态。
func (runner *Runner) Run(ctx context.Context, session *sess.AgentSession) error {
	if runner == nil || runner.client == nil || runner.executor == nil || session == nil {
		return fmt.Errorf("runner dependencies are incomplete")
	}
	if err := session.Start(time.Now().UTC()); err != nil {
		return err
	}
	runner.reportTurns = nil
	runner.findings = nil
	runner.findingSpecs = nil
	runner.sessionPool = sess.NewSessionPool()
	session.Sessions = nil
	runner.history = NewHistory(session.Target.Canonical, session.Intent)
	systemPrompt := buildSystemPrompt(runner.catalog)
	loadedSkills := make(map[string]bool)
	noCodeCount := 0
	refusalCount := 0
	hasExecutionEvidence := false
	lastFingerprint := ""
	fingerprintCount := 0

	for runner.config.MaxTurns <= 0 || session.Turn < runner.config.MaxTurns {
		if err := ctx.Err(); err != nil {
			_ = session.Cancel("cancelled", time.Now().UTC())
			return nil
		}
		response, err := runner.chat(ctx, agent.Request{SystemPrompt: systemPrompt, Messages: runner.history.Messages()})
		if err != nil {
			_ = session.Fail("provider_error", time.Now().UTC())
			session.AddEvent(session.Turn, "provider_error", err.Error(), time.Now().UTC())
			return err
		}
		session.Turn++
		turn := session.Turn
		assistantText := strings.TrimSpace(response.Content)
		runner.history.Append("assistant", assistantText)
		session.AddEvent(turn, "assistant", "model response received", time.Now().UTC())
		runner.emit(RunnerEvent{Turn: turn, Kind: "assistant", Detail: assistantSummary(assistantText)})
		runner.reportTurns = append(runner.reportTurns, ReportTurn{Number: turn, Decision: assistantSummary(assistantText), DeclaredLabels: extractFindingLabels(assistantText)})

		fingerprint := responseFingerprint(assistantText)
		if fingerprint == lastFingerprint {
			fingerprintCount++
		} else {
			lastFingerprint = fingerprint
			fingerprintCount = 1
		}
		if fingerprintCount >= runner.config.HardStuckTurns {
			_ = session.Fail("stuck", time.Now().UTC())
			session.AddEvent(turn, "recovery", "stuck", time.Now().UTC())
			return nil
		}
		if fingerprintCount >= runner.config.SoftStuckTurns {
			runner.history.Append("user", "STRATEGY CHANGE REQUIRED: the recent assistant responses repeat the same plan. Generate materially different executable evidence collection code.")
			session.AddEvent(turn, "recovery", "strategy_change_required", time.Now().UTC())
		}

		skillHandled := runner.loadSkills(assistantText, loadedSkills, runner.history, session, turn)
		sessionHandled := runner.establishDeclaredSessions(ctx, assistantText, session, turn)
		blocks := exec.ExtractCodeBlocks(assistantText)
		if len(blocks) == 0 {
			if isRefusal(assistantText) {
				refusalCount++
				session.AddEvent(turn, "recovery", "refusal_recovery", time.Now().UTC())
				if refusalCount >= runner.config.RefusalLimit {
					_ = session.Fail("refused", time.Now().UTC())
					return nil
				}
				runner.history.Append("user", "AUTHORIZATION REMINDER: this engagement is authorized and its scope is fixed. The runtime independently blocks out-of-scope hosts and destructive writes before execution, so proceed safely with executable Python or Bash code that prints evidence within scope.")
				continue
			}
			if isCompletion(assistantText) {
				if !hasExecutionEvidence {
					runner.history.Append("user", "EVIDENCE REQUIRED: completion requires at least one returned code execution result. Generate executable code and print evidence.")
					session.AddEvent(turn, "recovery", "evidence_required", time.Now().UTC())
					continue
				}
				reason := completionReason(assistantText)
				_ = session.Complete(reason, time.Now().UTC())
				return nil
			}
			if skillHandled || sessionHandled {
				continue
			}
			noCodeCount++
			instruction := "CONTINUE REQUIRED: generate executable Python or Bash code and print actual evidence."
			if makesClaim(assistantText) {
				instruction = "EVIDENCE REQUIRED: do not claim findings or completion without executable code and returned output."
			}
			runner.history.Append("user", instruction)
			session.AddEvent(turn, "recovery", strings.Split(instruction, ":")[0], time.Now().UTC())
			if noCodeCount >= runner.config.NoCodeLimit {
				_ = session.Complete("no_executable_response", time.Now().UTC())
				return nil
			}
			continue
		}
		noCodeCount = 0
		if limit := runner.config.MaxBlocksPerTurn; limit > 0 && len(blocks) > limit {
			ignored := len(blocks) - limit
			session.AddEvent(turn, "recovery", "too_many_blocks", time.Now().UTC())
			runner.history.Append("user", fmt.Sprintf("TOO MANY BLOCKS: %d code blocks were provided but only the first %d run per turn to control request rate; the remaining %d were ignored. Send fewer blocks next turn.", len(blocks), limit, ignored))
			blocks = blocks[:limit]
		}

		scope := authz.NewScope(hostOf(session.Target.Canonical), runner.config.AllowedHosts, runner.config.AllowPrivateHosts)
		preflight := make([]exec.PreflightResult, 0, len(blocks))
		approved := make([]exec.PreflightResult, 0, len(blocks))
		authorizationBlocked := false
		for _, block := range blocks {
			result := exec.Preflight(block)
			if result.Approved {
				if decision := runner.config.Authorizer.Authorize(result.Block, scope); !decision.Allowed {
					result.Approved = false
					result.Rejection = decision.Reason
					authorizationBlocked = true
				}
			}
			preflight = append(preflight, result)
			if result.Approved {
				approved = append(approved, result)
			}
		}
		if authorizationBlocked {
			session.AddEvent(turn, "recovery", "authorization_blocked", time.Now().UTC())
		}
		if len(approved) == 0 {
			extraEnv := runner.sessionEnv()
			results := runner.executor.Execute(ctx, exec.ExecutionInput{
				SessionID: session.ID,
				Target:    session.Target.Canonical,
				Turn:      turn,
				Blocks:    preflight,
				ExtraEnv:  extraEnv,
			})
			runner.recordReportBlocks(redactExecutionResults(results, extraEnv))
			runner.history.Append("user", renderPreflightRejections(turn, preflight))
			session.AddEvent(turn, "recovery", "preflight_rejected", time.Now().UTC())
			continue
		}

		for _, block := range approved {
			runner.emit(RunnerEvent{Turn: turn, Kind: "block_started", BlockIndex: block.Block.Index, Detail: string(block.Block.Language)})
		}
		extraEnv := runner.sessionEnv()
		results := runner.executor.Execute(ctx, exec.ExecutionInput{
			SessionID: session.ID,
			Target:    session.Target.Canonical,
			Turn:      turn,
			Blocks:    preflight,
			ExtraEnv:  extraEnv,
		})
		if len(approved) > 0 {
			hasExecutionEvidence = true
		}
		for _, result := range results {
			runner.emit(RunnerEvent{Turn: turn, Kind: "block_finished", BlockIndex: result.Block.Index, Detail: string(result.Status)})
		}
		safeResults := redactExecutionResults(results, extraEnv)
		runner.recordReportBlocks(safeResults)
		resultText := RenderExecutionResults(turn, safeResults)
		runner.history.Append("user", resultText)
		session.AddEvent(turn, "execution", fmt.Sprintf("%d block(s)", len(results)), time.Now().UTC())
		if allNoOutput(results) {
			runner.history.Append("user", "SCRIPT NO OUTPUT: all executed blocks produced no stdout or stderr. Rewrite the code to perform an operation and print evidence.")
			session.AddEvent(turn, "recovery", "script_no_output", time.Now().UTC())
		}
		if hasNetworkFriction(results) {
			if err := runner.sleep(ctx, runner.config.NetworkBackoff); err != nil {
				_ = session.Cancel("cancelled", time.Now().UTC())
				return nil
			}
			runner.history.Append("user", "NETWORK FRICTION: execution output indicates throttling or transport failure. Adjust rate, target, or strategy before continuing.")
			session.AddEvent(turn, "recovery", "network_friction", time.Now().UTC())
		}
	}
	_ = session.Fail("max_turns", time.Now().UTC())
	return nil
}

func (runner *Runner) sessionEnv() map[string]string {
	if runner == nil || runner.sessionPool == nil {
		return nil
	}
	return runner.sessionPool.ExportCookieEnv()
}

func redactExecutionResults(results []exec.ExecutionResult, extraEnv map[string]string) []exec.ExecutionResult {
	if len(results) == 0 {
		return nil
	}
	redacted := append([]exec.ExecutionResult(nil), results...)
	for index := range redacted {
		redacted[index].Stdout = exec.RedactSessionSecrets(redacted[index].Stdout, extraEnv)
		redacted[index].Stderr = exec.RedactSessionSecrets(redacted[index].Stderr, extraEnv)
		redacted[index].Error = exec.RedactSessionSecrets(redacted[index].Error, extraEnv)
	}
	return redacted
}

const findingConsolidationSystemPrompt = `You consolidate completed PentGo engagements into framework verification declarations. Only execution evidence may support a declaration. For each candidate vulnerability, output exactly one block in this format:
=== PENTGO FINDING ===
type: xss
method: GET
url: https://target.example/path?payload=value
baseline_url: https://target.example/path?payload=benign
payload: payload=value
description: concise evidence-backed claim
=== END PENTGO FINDING ===
Use only supported types: sqli, xss, lfi, rce, auth_bypass, credential, idor, upload, open_redirect.
For a credential finding, declare the observed login flow in the block:
type: credential
login_url: https://target.example/login
login_method: POST
login_body: username=observed&password=observed
login_content_type: application/x-www-form-urlencoded
username: observed
The login_method, login_content_type, and username fields are optional. For findings that need an authenticated session (such as authorization differences), include login_url and login_body in the same block; the framework logs in and carries the resulting session only for the payload request.
For horizontal IDOR (bingo two-user mode), declare both sessions and the object URL:
type: idor
method: GET
url: https://target.example/user/2
login_url: https://target.example/login
login_body: username=userA&password=observed
username: userA
login_url_b: https://target.example/login
login_body_b: username=userB&password=observed
username_b: userB
The framework logs in as A for the payload request and as B for the baseline (same URL when baseline_url is omitted), then scores a meaningful response diff. Declare login endpoints and credentials only when they appear in returned execution evidence; never infer or invent them. Output no block when no evidence-backed candidate exists. Do not output code or prose outside the blocks.`

// ConsolidateAndVerify asks for structured declarations once an engagement is
// done, then delegates each final verdict to the framework verifier.
func (runner *Runner) ConsolidateAndVerify(ctx context.Context, session *sess.AgentSession) []verify.VerificationResult {
	if runner == nil || runner.client == nil || runner.history == nil || runner.config.Verifier == nil || session == nil || session.Status != sess.SessionDone || ctx.Err() != nil {
		return nil
	}
	runner.findings = nil
	runner.findingSpecs = nil
	runner.history.Append("user", "Consolidate evidence-backed vulnerability candidates into PENTGO FINDING blocks for independent framework verification.")
	response, err := runner.chat(ctx, agent.Request{SystemPrompt: findingConsolidationSystemPrompt, Messages: runner.history.Messages()})
	if err != nil {
		session.AddEvent(session.Turn, "verification_consolidation_error", err.Error(), time.Now().UTC())
		return nil
	}
	specs := verify.ParseFindingSpecs(response.Content)
	if len(specs) > runner.config.MaxFindings {
		specs = specs[:runner.config.MaxFindings]
	}
	runner.findingSpecs = append([]verify.FindingSpec(nil), specs...)
	for index, spec := range specs {
		result, record := runner.verifyFinding(ctx, spec, session)
		if result.VulnType == "" {
			result.VulnType = spec.VulnType
		}
		if result.Curl == "" {
			if spec.VulnType == verify.VulnCredential {
				result.Curl = verify.LoginCurlCommand(spec)
			} else {
				result.Curl = verify.CurlCommand(spec)
			}
		}
		runner.persistVerificationEvidence(session, index+1, &result, record)
		runner.findings = append(runner.findings, result)
	}
	session.Findings = append([]verify.VerificationResult(nil), runner.findings...)
	session.AddEvent(session.Turn, "verification_consolidated", fmt.Sprintf("%d finding(s)", len(runner.findings)), time.Now().UTC())
	return append([]verify.VerificationResult(nil), runner.findings...)
}

func (runner *Runner) verifyFinding(ctx context.Context, spec verify.FindingSpec, session *sess.AgentSession) (verify.VerificationResult, verify.VerificationRecord) {
	if verifier, ok := runner.config.Verifier.(optionsFindingVerifier); ok {
		return verifier.VerifyWithEvidenceOptions(ctx, spec, runner.verifyOptions(spec, session))
	}
	return runner.config.Verifier.VerifyWithEvidence(ctx, spec)
}

func (runner *Runner) verifyOptions(spec verify.FindingSpec, session *sess.AgentSession) verify.VerifyOptions {
	options := verify.VerifyOptions{}
	if runner == nil || runner.sessionPool == nil {
		return options
	}
	nameA := sess.SessionNameFromIdentity(spec.SessionName, spec.Username, "default")
	if cached, ok := runner.sessionPool.Get(nameA); ok {
		options.CookieA = cached.CookieHeader
		options.CookieNamesA = append([]string(nil), cached.CookieNames...)
	} else if strings.TrimSpace(spec.LoginURL) != "" {
		options.OnLoginA = func(result verify.LoginResult) {
			runner.rememberVerifiedLogin(session, nameA, spec.Username, spec.LoginURL, result)
		}
	}

	nameB := sess.SessionNameFromIdentity(spec.SessionNameB, spec.UsernameB, "default_b")
	if cached, ok := runner.sessionPool.Get(nameB); ok {
		options.CookieB = cached.CookieHeader
		options.CookieNamesB = append([]string(nil), cached.CookieNames...)
	} else if strings.TrimSpace(spec.LoginURLB) != "" {
		options.OnLoginB = func(result verify.LoginResult) {
			runner.rememberVerifiedLogin(session, nameB, spec.UsernameB, spec.LoginURLB, result)
		}
	}
	return options
}

func (runner *Runner) rememberVerifiedLogin(session *sess.AgentSession, name, username, loginURL string, result verify.LoginResult) {
	if runner == nil || runner.sessionPool == nil || name == "" {
		return
	}
	runner.sessionPool.Put(&sess.AuthSession{
		Name:             name,
		Username:         username,
		LoginURL:         loginURL,
		CookieHeader:     result.SessionCookieHeader,
		CookieNames:      append([]string(nil), result.CookieNames...),
		MeaningfulCookie: result.MeaningfulCookie,
		LoginStatus:      result.StatusCode,
		Verified:         result.Verified,
		CSRFToken:        result.CSRFToken,
		EstablishedAt:    time.Now().UTC(),
	})
	if session != nil {
		session.Sessions = runner.sessionPool.PublicView()
	}
}

func (runner *Runner) persistVerificationEvidence(session *sess.AgentSession, index int, result *verify.VerificationResult, record verify.VerificationRecord) {
	if runner.config.EvidenceSink == nil || result == nil {
		return
	}
	evidence := verificationEvidence{
		SchemaVersion:          "1",
		VulnType:               result.VulnType,
		Verdict:                result.Verdict,
		Confidence:             result.Confidence,
		Method:                 record.Method,
		PayloadURL:             record.PayloadURL,
		BaselineURL:            record.BaselineURL,
		RequestHeaders:         redactCookieHeaders(record.RequestHeaders),
		RequestBody:            record.RequestBody,
		BaselineRequestBody:    record.BaselineRequestBody,
		PayloadStatus:          record.PayloadStatus,
		PayloadResponseBody:    record.PayloadResponseBody,
		PayloadLocation:        record.PayloadLocation,
		BaselineStatus:         record.BaselineStatus,
		BaselineResponseBody:   record.BaselineResponseBody,
		Reproductions:          record.Reproductions,
		ScopeRejected:          record.ScopeRejected,
		LoginAttempted:         record.LoginAttempted,
		LoginVerified:          record.LoginVerified,
		LoginStatus:            record.LoginStatus,
		LoginCookieNames:       append([]string(nil), record.LoginCookieNames...),
		LoginMeaningfulCookie:  record.LoginMeaningfulCookie,
		LoginSnippet:           record.LoginSnippet,
		LoginBAttempted:        record.LoginBAttempted,
		LoginBVerified:         record.LoginBVerified,
		LoginBStatus:           record.LoginBStatus,
		LoginBCookieNames:      append([]string(nil), record.LoginBCookieNames...),
		LoginBMeaningfulCookie: record.LoginBMeaningfulCookie,
		LoginBSnippet:          record.LoginBSnippet,
		IDORDiffReason:         result.IDORDiffReason,
		ChecksPassed:           append([]string(nil), result.ChecksPassed...),
		ChecksFailed:           append([]string(nil), result.ChecksFailed...),
		Curl:                   result.Curl,
	}
	name := fmt.Sprintf("verification-%03d", index)
	path, err := runner.config.EvidenceSink.WriteEvidence(name, evidence)
	if err != nil {
		if session != nil {
			session.AddEvent(session.Turn, "verification_evidence_error", err.Error(), time.Now().UTC())
		}
		return
	}
	result.EvidencePath = path
}

func redactCookieHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	redacted := make(map[string]string, len(headers))
	for key, value := range headers {
		if strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "Set-Cookie") {
			redacted[key] = "[redacted]"
			continue
		}
		redacted[key] = value
	}
	return redacted
}

// ReportContext 返回本次 Runner 执行收集的有界、无代码报告上下文副本。
func (runner *Runner) ReportContext(session *sess.AgentSession) ReportContext {
	context := ReportContext{}
	if session != nil {
		context.Target = session.Target.Canonical
		context.Intent = session.Intent
		context.Status = session.Status
		context.StopReason = session.StopReason
		context.Skills = append([]string(nil), session.LoadedSkills...)
		for _, event := range session.Timeline {
			if event.Kind == "recovery" || event.Kind == "provider_error" {
				context.RecoveryEvents = append(context.RecoveryEvents, event)
			}
		}
	}
	context.VerifiedFindings = make([]verify.VerificationResult, len(runner.findings))
	for index, finding := range runner.findings {
		context.VerifiedFindings[index] = finding
		context.VerifiedFindings[index].ChecksPassed = append([]string(nil), finding.ChecksPassed...)
		context.VerifiedFindings[index].ChecksFailed = append([]string(nil), finding.ChecksFailed...)
	}
	context.Turns = make([]ReportTurn, len(runner.reportTurns))
	for index, turn := range runner.reportTurns {
		context.Turns[index] = ReportTurn{Number: turn.Number, Decision: turn.Decision, DeclaredLabels: append([]exec.EvidenceLevel(nil), turn.DeclaredLabels...), Blocks: append([]ReportBlock(nil), turn.Blocks...)}
	}
	return context
}

func (runner *Runner) recordReportBlocks(results []exec.ExecutionResult) {
	if len(runner.reportTurns) == 0 {
		return
	}
	turn := &runner.reportTurns[len(runner.reportTurns)-1]
	for _, result := range results {
		turn.Blocks = append(turn.Blocks, ReportBlock{
			Index:        result.Block.Index,
			Language:     result.Block.Language,
			Status:       result.Status,
			ExitCode:     result.ExitCode,
			Stdout:       truncateBytes(result.Stdout, maxReportBlockOutputBytes),
			Stderr:       truncateBytes(result.Stderr, maxReportBlockOutputBytes),
			EvidencePath: result.EvidencePath,
			Level:        result.Level,
		})
	}
}

func (runner *Runner) chat(ctx context.Context, request agent.Request) (agent.Response, error) {
	response, err := runner.client.Chat(ctx, request)
	if err == nil {
		return response, nil
	}
	if sleepErr := runner.sleep(ctx, runner.config.ProviderRetryDelay); sleepErr != nil {
		return agent.Response{}, err
	}
	return runner.client.Chat(ctx, request)
}

func (runner *Runner) loadSkills(text string, loaded map[string]bool, history *History, session *sess.AgentSession, turn int) bool {
	matches := skillLoadPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		name := match[1]
		if loaded[name] {
			history.Append("user", "SKILL_LOAD RESULT: skill "+name+" was already loaded for this engagement.")
			continue
		}
		prompt, err := runner.load(name)
		if err != nil {
			history.Append("user", "SKILL_LOAD RESULT: "+err.Error())
			continue
		}
		loaded[name] = true
		session.LoadedSkills = append(session.LoadedSkills, name)
		history.Append("user", "=== PENTGO SKILL CONTEXT ===\nskill: "+name+"\n"+prompt+"\n=== END PENTGO SKILL CONTEXT ===")
		session.AddEvent(turn, "skill_loaded", name, time.Now().UTC())
	}
	return true
}

// RenderExecutionResults 生成下一轮模型可直接读取的统一证据文本。
func RenderExecutionResults(turn int, results []exec.ExecutionResult) string {
	var builder strings.Builder
	for _, result := range results {
		fmt.Fprintf(&builder, "=== PENTGO EXECUTION RESULT ===\nturn: %d\nlanguage: %s\nstatus: %s\nexit_code: %d\nstdout:\n%sstderr:\n%s", turn, result.Block.Language, result.Status, result.ExitCode, ensureTrailingNewline(result.Stdout), ensureTrailingNewline(result.Stderr))
		if result.Error != "" {
			fmt.Fprintf(&builder, "error:\n%s\n", result.Error)
		}
		if result.EvidencePath != "" {
			fmt.Fprintf(&builder, "evidence:\n- %s\n", result.EvidencePath)
		}
		builder.WriteString("=== END PENTGO EXECUTION RESULT ===")
	}
	return builder.String()
}

func renderPreflightRejections(turn int, results []exec.PreflightResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "PREFLIGHT REJECTED\nturn: %d\n", turn)
	for _, result := range results {
		fmt.Fprintf(&builder, "block: %d\nlanguage: %s\nreason: %s\n", result.Block.Index, result.Block.Language, result.Rejection)
	}
	builder.WriteString("Rewrite the rejected code as executable Python or Bash.")
	return builder.String()
}

func ensureTrailingNewline(value string) string {
	if value == "" {
		return "\n"
	}
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func isCompletion(text string) bool {
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "TASK_COMPLETE") || strings.Contains(upper, "MISSION_COMPLETE")
}

func completionReason(text string) string {
	if strings.Contains(strings.ToUpper(text), "MISSION_COMPLETE") {
		return "mission_complete"
	}
	return "task_complete"
}

func makesClaim(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{"found", "finding", "vulnerability", "success", "发现", "漏洞", "成功"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func responseFingerprint(text string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

func allNoOutput(results []exec.ExecutionResult) bool {
	if len(results) == 0 {
		return true
	}
	for _, result := range results {
		if strings.TrimSpace(result.Stdout) != "" || strings.TrimSpace(result.Stderr) != "" {
			return false
		}
	}
	return true
}

func hasNetworkFriction(results []exec.ExecutionResult) bool {
	for _, result := range results {
		value := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + result.Error)
		for _, marker := range []string{"429", "403", "503", "connection reset", "timeout", "timed out"} {
			if strings.Contains(value, marker) {
				return true
			}
		}
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (runner *Runner) emit(event RunnerEvent) {
	if runner != nil && runner.config.OnEvent != nil {
		runner.config.OnEvent(event)
	}
}

func assistantSummary(text string) string {
	insideCode := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			insideCode = !insideCode
			continue
		}
		if !insideCode && line != "" {
			if len(line) > 160 {
				return line[:160]
			}
			return line
		}
	}
	if len(exec.ExtractCodeBlocks(text)) > 0 {
		return "code blocks received"
	}
	return "response received"
}

func hostOf(canonical string) string {
	parsed, err := url.Parse(canonical)
	if err != nil {
		return canonical
	}
	return parsed.Hostname()
}
