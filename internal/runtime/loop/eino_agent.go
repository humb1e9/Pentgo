package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/exec"

	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// einoToolSet owns the server-side state shared by the ADK tools that back the
// openai engagement loop. Because ADK's AsyncIterator is an unbounded channel,
// the event-consumer loop (see eino_run_loop.go) runs concurrently with tool
// execution; every tool here is therefore self-contained and mutex-guarded, and
// it stashes structured ExecutionResults in a FIFO the event loop pops in order.
//
// It reuses the exact PentGo pipeline the hand-rolled runner uses:
// Preflight -> Authorizer.Authorize -> executor.Execute -> RedactSessionSecrets.
type einoToolSet struct {
	executor       BlockExecutor
	authorizer     *authz.Authorizer
	scope          authz.Scope
	sessionID      string
	target         string
	load           SkillLoader
	establisher    sessionEstablisher
	sessionPool    *sess.SessionPool
	sleep          Sleeper
	networkBackoff time.Duration
	onBlockEvent   func(RunnerEvent)

	mu             sync.Mutex
	turn           int
	hasEvidence    bool
	resultStash    []einoExecOutcome // FIFO of per-call redacted results
	sessionResults []string          // FIFO of declare_session render text
}

// einoExecOutcome is one execute_code call's redacted results plus whether the
// authorizer blocked an otherwise-approved block. The event loop pops these in
// source order and emits the authorization_blocked recovery event the text-path
// runner emits (runner.go), keeping the timeline contract identical.
type einoExecOutcome struct {
	results      []exec.ExecutionResult
	authzBlocked bool
}

// executeCodeArgs is the single tool argument surface: the model "writes code"
// but it arrives here as tool-call arguments, decoded by InferTool.
type executeCodeArgs struct {
	Language string `json:"language" jsonschema:"description=Interpreter for the code: python or shell,enum=python,enum=shell"`
	Code     string `json:"code" jsonschema:"description=The complete program to run. Print evidence to stdout."`
}

type loadSkillArgs struct {
	Name string `json:"name" jsonschema:"description=Registered skill name to load into context."`
}

type declareSessionArgs struct {
	Name             string `json:"name" jsonschema:"description=Unique session label, e.g. low_priv or admin."`
	Role             string `json:"role" jsonschema:"description=Role hint, e.g. user or admin."`
	Username         string `json:"username" jsonschema:"description=Username used for the login."`
	LoginURL         string `json:"login_url" jsonschema:"description=Absolute URL of the login endpoint."`
	LoginMethod      string `json:"login_method" jsonschema:"description=HTTP method for login, usually POST."`
	LoginBody        string `json:"login_body" jsonschema:"description=Login request body, e.g. username=x&password=y."`
	LoginContentType string `json:"login_content_type" jsonschema:"description=Content-Type of the login body."`
}

type completeTaskArgs struct {
	FinalResult string `json:"final_result" jsonschema:"description=The final engagement conclusion to return. Summarize verified findings and evidence."`
}

const (
	executeCodeToolDesc    = "Run a self-contained python or shell program against the engagement target and return its stdout/stderr. This is the only way to gather evidence: print the observations you rely on. Session cookies declared via declare_session are injected into the child environment automatically."
	loadSkillToolDesc      = "Load a registered read-only skill by name into the conversation to consult its guidance before acting."
	declareSessionToolDesc = "Authenticate an identity through the framework login verifier and add it to the session pool. Its cookies become available to subsequent execute_code calls. Declare both a low-privilege and a high-privilege identity for vertical privilege-escalation checks."
	evidenceGateToolDesc   = "Internal framework gate. Do not call directly; the framework reroutes premature completions here to remind you to gather executed evidence first."
)

// popResults returns and clears the oldest stashed execution-result batch.
// The event loop calls this on each tool-result event to preserve source order.
func (ts *einoToolSet) popResults() (einoExecOutcome, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.resultStash) == 0 {
		return einoExecOutcome{}, false
	}
	batch := ts.resultStash[0]
	ts.resultStash = ts.resultStash[1:]
	return batch, true
}

// popSessionResult returns the oldest stashed declare_session render text.
func (ts *einoToolSet) popSessionResult() (string, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.sessionResults) == 0 {
		return "", false
	}
	text := ts.sessionResults[0]
	ts.sessionResults = ts.sessionResults[1:]
	return text, true
}

// evidenceSeen reports whether any execute_code call has produced approved
// execution output. The exit-gate middleware consults this.
func (ts *einoToolSet) evidenceSeen() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.hasEvidence
}

// sessionEnv exports the session pool's cookie environment for child processes.
// It mirrors (*Runner).sessionEnv: values enter only the child env, never the
// ADK history or evidence JSON.
func (ts *einoToolSet) sessionEnv() map[string]string {
	if ts.sessionPool == nil {
		return nil
	}
	return ts.sessionPool.ExportCookieEnv()
}

// supportedToolLanguage mirrors exec.supportedLanguage (unexported) for the
// tool-argument surface, mapping the model's language string to a CodeBlock
// language. Unknown languages are a soft error the model can self-correct.
func supportedToolLanguage(value string) (exec.Language, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "python", "python3":
		return exec.LanguagePython, true
	case "bash", "sh", "shell":
		return exec.LanguageShell, true
	default:
		return "", false
	}
}

// executeCode is the InvokeFunc backing the execute_code tool. It runs one code
// block through the identical pipeline the hand-rolled runner uses, redacts
// session secrets before returning, stashes the structured result for the event
// loop, and applies network backoff in-band (the tool runs synchronously inside
// the ADK graph, so sleeping here throttles the loop exactly as the runner did).
func (ts *einoToolSet) executeCode(ctx context.Context, args executeCodeArgs) (string, error) {
	language, ok := supportedToolLanguage(args.Language)
	if !ok {
		// Soft error: nil error keeps the ADK graph alive so the model can retry.
		return fmt.Sprintf("EXECUTE_CODE REJECTED: unsupported language %q; use \"python\" or \"shell\".", args.Language), nil
	}

	ts.mu.Lock()
	ts.turn++
	turn := ts.turn
	ts.mu.Unlock()

	block := exec.CodeBlock{Index: 1, Language: language, Code: args.Code}
	preflight := exec.Preflight(block)
	authzBlocked := false
	if preflight.Approved {
		if decision := ts.authorizer.Authorize(preflight.Block, ts.scope); !decision.Allowed {
			preflight.Approved = false
			preflight.Rejection = decision.Reason
			authzBlocked = true
		}
	}
	approved := preflight.Approved

	if ts.onBlockEvent != nil && approved {
		ts.onBlockEvent(RunnerEvent{Turn: turn, Kind: "block_started", BlockIndex: block.Index, Detail: string(language)})
	}

	extraEnv := ts.sessionEnv()
	results := ts.executor.Execute(ctx, exec.ExecutionInput{
		SessionID: ts.sessionID,
		Target:    ts.target,
		Turn:      turn,
		Blocks:    []exec.PreflightResult{preflight},
		ExtraEnv:  extraEnv,
	})
	safeResults := redactExecutionResults(results, extraEnv)

	if ts.onBlockEvent != nil {
		for _, result := range safeResults {
			ts.onBlockEvent(RunnerEvent{Turn: turn, Kind: "block_finished", BlockIndex: result.Block.Index, Detail: string(result.Status)})
		}
	}

	ts.mu.Lock()
	ts.resultStash = append(ts.resultStash, einoExecOutcome{results: safeResults, authzBlocked: authzBlocked})
	if approved {
		ts.hasEvidence = true
	}
	ts.mu.Unlock()

	if !approved {
		return renderPreflightRejections(turn, []exec.PreflightResult{preflight}), nil
	}

	rendered := RenderExecutionResults(turn, safeResults)
	if hasNetworkFriction(results) {
		if err := ts.sleep(ctx, ts.networkBackoff); err != nil {
			return rendered, err
		}
		rendered += "\nNETWORK FRICTION: execution output indicates throttling or transport failure. Adjust rate, target, or strategy before continuing."
	}
	return rendered, nil
}

// loadSkill backs the load_skill tool, replacing the SKILL_LOAD: text convention.
func (ts *einoToolSet) loadSkill(_ context.Context, args loadSkillArgs) (string, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" || ts.load == nil {
		return "LOAD_SKILL REJECTED: provide a registered skill name.", nil
	}
	content, err := ts.load(name)
	if err != nil {
		return fmt.Sprintf("LOAD_SKILL REJECTED: %v", err), nil
	}
	return content, nil
}

// declareSession backs the declare_session tool, replacing the PENTGO SESSION
// text grammar. It runs the framework login verifier and adds a verified
// identity to the session pool so its cookies reach later execute_code calls.
func (ts *einoToolSet) declareSession(ctx context.Context, args declareSessionArgs) (string, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return "DECLARE_SESSION REJECTED: provide a unique session name.", nil
	}
	if ts.sessionPool == nil {
		ts.sessionPool = sess.NewSessionPool()
	}
	if _, ok := ts.sessionPool.Get(name); ok {
		text := "SESSION RESULT: " + name + " verified (reused)"
		ts.stashSessionResult(text)
		return text, nil
	}
	if ts.establisher == nil {
		text := "SESSION RESULT: " + name + " failed (framework verifier unavailable)"
		ts.stashSessionResult(text)
		return text, nil
	}

	spec := SessionSpec{
		Name:             name,
		Role:             args.Role,
		Username:         args.Username,
		LoginURL:         args.LoginURL,
		LoginMethod:      args.LoginMethod,
		LoginBody:        args.LoginBody,
		LoginContentType: args.LoginContentType,
	}
	outcome := ts.establisher.EstablishSession(ctx, spec.toLoginSpec())
	identity := &sess.AuthSession{
		Name:             spec.Name,
		Role:             spec.Role,
		Username:         spec.Username,
		LoginURL:         spec.LoginURL,
		CookieHeader:     outcome.SessionCookieHeader,
		CookieNames:      append([]string(nil), outcome.CookieNames...),
		MeaningfulCookie: outcome.MeaningfulCookie,
		LoginStatus:      outcome.StatusCode,
		Verified:         outcome.Verified,
		CSRFToken:        outcome.CSRFToken,
		EstablishedAt:    time.Now().UTC(),
	}
	ts.sessionPool.Put(identity)

	var text string
	if outcome.Verified && identity.CookieHeader != "" {
		text = fmt.Sprintf("SESSION RESULT: %s verified; cookie names: %s", spec.Name, strings.Join(identity.CookieNames, ","))
	} else {
		text = "SESSION RESULT: " + spec.Name + " failed"
	}
	ts.stashSessionResult(text)
	return text, nil
}

func (ts *einoToolSet) stashSessionResult(text string) {
	ts.mu.Lock()
	ts.sessionResults = append(ts.sessionResults, text)
	ts.mu.Unlock()
}

const completeTaskToolName = "complete_task"
const evidenceGateToolName = "evidence_gate"

// completeTaskExitTool is a custom ADK Exit tool named "complete_task". It is
// wired via ChatModelAgentConfig.Exit, so ADK registers it as return-directly:
// when the model calls it, toolPreHandle sets the tool->END tripwire and the
// react graph terminates cleanly at the tool node instead of looping back to the
// model. That clean termination is mandatory — a plain (non-return-directly)
// tool leaves the eager react graph free to fire another model request before
// the event loop can observe Action.Exit, which drains and over-runs the model.
// InvokableRun emits Action.Exit (via SendToolGenAction) so RunEino's event loop
// sees event.Action.Exit and completes the session.
type completeTaskExitTool struct{}

func (completeTaskExitTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: completeTaskToolName,
		Desc: "End the engagement and return the final result. Call this only after execute_code has returned evidence supporting your conclusion.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"final_result": {
				Desc:     "The final engagement conclusion: verified findings and their evidence.",
				Required: true,
				Type:     schema.String,
			},
		}),
	}, nil
}

func (completeTaskExitTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args completeTaskArgs
	// Best-effort decode: the exit action must fire even if arguments are sparse.
	_ = json.Unmarshal([]byte(argumentsInJSON), &args)
	if err := adk.SendToolGenAction(ctx, completeTaskToolName, adk.NewExitAction()); err != nil {
		return "", fmt.Errorf("emit complete_task exit action: %w", err)
	}
	result := strings.TrimSpace(args.FinalResult)
	if result == "" {
		result = "task complete"
	}
	return result, nil
}

// evidenceGate backs the internal evidence_gate tool. It is NOT the exit tool and
// is NOT return-directly, so when the gate middleware reroutes a premature
// complete_task call here, the tool executes, returns this nudge, and the react
// graph loops back to the model (the graph only re-enters the model after a tool
// runs). The model then self-corrects and gathers evidence before completing.
func (ts *einoToolSet) evidenceGate(_ context.Context, _ completeTaskArgs) (string, error) {
	return "EVIDENCE REQUIRED: do not complete the task or claim findings without executable code and returned output. Call execute_code to gather evidence first, then complete_task.", nil
}

// evidenceGateMiddleware enforces PentGo's "no completion without evidence" rule.
// AfterModelRewriteState runs after the model call with the response as the last
// message and before the tool-call branch and toolPreHandle read it (Generate
// returns the rewritten last message). If the model called complete_task before
// any approved execute_code output exists, the middleware renames those calls to
// evidence_gate: the return-directly tripwire (keyed on the complete_task name)
// never fires, evidence_gate runs and nudges, and the graph loops back so the
// model self-corrects instead of terminating the run prematurely.
type evidenceGateMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	tools *einoToolSet
}

func newEvidenceGateMiddleware(tools *einoToolSet) *evidenceGateMiddleware {
	return &evidenceGateMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		tools:                        tools,
	}
}

func (m *evidenceGateMiddleware) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 || m.tools.evidenceSeen() {
		return ctx, state, nil
	}
	last := state.Messages[len(state.Messages)-1]
	if last == nil || last.Role != schema.Assistant || len(last.ToolCalls) == 0 {
		return ctx, state, nil
	}
	for i := range last.ToolCalls {
		if last.ToolCalls[i].Function.Name == completeTaskToolName {
			last.ToolCalls[i].Function.Name = evidenceGateToolName
		}
	}
	return ctx, state, nil
}

// literalInstructionGenModelInput passes the instruction through as a system
// message without FString template formatting. Eino's defaultGenModelInput
// formats the instruction whenever SessionValues exist; PentGo prompts carry
// literal curly braces (JSON examples, payload templates) that then crash with
// "could not find key". This mirrors eino/adk/prebuilt's supported workaround.
func literalInstructionGenModelInput(_ context.Context, instruction string, input *adk.AgentInput) ([]adk.Message, error) {
	messages := make([]adk.Message, 0, len(input.Messages)+1)
	if instruction != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	messages = append(messages, input.Messages...)
	return messages, nil
}

// buildTools infers the real tools plus the internal evidence_gate tool. The
// completion tool (complete_task) is NOT here — it is the custom return-directly
// Exit tool supplied via ChatModelAgentConfig.Exit. evidence_gate is the reroute
// target the gate middleware sends premature complete_task calls to; it is a
// normal (non-return-directly) tool so the graph loops back to the model.
func (ts *einoToolSet) buildTools() ([]tool.BaseTool, error) {
	execTool, err := toolutils.InferTool("execute_code", executeCodeToolDesc, ts.executeCode)
	if err != nil {
		return nil, fmt.Errorf("infer execute_code tool: %w", err)
	}
	skillTool, err := toolutils.InferTool("load_skill", loadSkillToolDesc, ts.loadSkill)
	if err != nil {
		return nil, fmt.Errorf("infer load_skill tool: %w", err)
	}
	sessionTool, err := toolutils.InferTool("declare_session", declareSessionToolDesc, ts.declareSession)
	if err != nil {
		return nil, fmt.Errorf("infer declare_session tool: %w", err)
	}
	gateTool, err := toolutils.InferTool(evidenceGateToolName, evidenceGateToolDesc, ts.evidenceGate)
	if err != nil {
		return nil, fmt.Errorf("infer evidence_gate tool: %w", err)
	}
	return []tool.BaseTool{execTool, skillTool, sessionTool, gateTool}, nil
}

// newEinoAgent assembles the ADK ChatModelAgent for the openai engagement path:
// the code-as-tool-argument agent with the literal-instruction input transform.
// Completion is a custom return-directly Exit tool (completeTaskExitTool) named
// complete_task, gated by evidenceGateMiddleware which reroutes premature
// completions to the evidence_gate tool so the graph loops back for evidence.
// maxTurns maps to MaxIterations.
func newEinoAgent(ctx context.Context, chatModel model.ToolCallingChatModel, instruction string, maxTurns int, tools *einoToolSet) (*adk.ChatModelAgent, error) {
	toolList, err := tools.buildTools()
	if err != nil {
		return nil, err
	}
	if maxTurns <= 0 {
		maxTurns = 20
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "pentgo-openai-engagement",
		Description:   "PentGo engagement agent driving code execution and framework verification.",
		Instruction:   instruction,
		Model:         chatModel,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList}},
		GenModelInput: literalInstructionGenModelInput,
		Exit:          completeTaskExitTool{},
		MaxIterations: maxTurns,
		Handlers:      []adk.ChatModelAgentMiddleware{newEvidenceGateMiddleware(tools)},
	})
}
