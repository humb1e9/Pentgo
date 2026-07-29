package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/evidence"
	"pentgo/internal/runtime/exec"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type execArgs struct {
	Command string `json:"command" jsonschema:"description=Complete Bash command to run in the engagement work directory."`
}
type executePythonArgs struct {
	Script string `json:"script" jsonschema:"description=Complete Python program to run with python3 -u in the engagement work directory."`
}
type loadSkillArgs struct {
	Name string `json:"name" jsonschema:"description=Registered PentGo skill name."`
}
type recordFindingArgs struct {
	Title          string `json:"title" jsonschema:"description=Concise unique finding title."`
	Severity       string `json:"severity" jsonschema:"description=One of critical high medium low info."`
	Description    string `json:"description" jsonschema:"description=Evidence-backed description of the observed issue."`
	EvidenceRefs   []int  `json:"evidence_refs" jsonschema:"description=Successful evidence sequence numbers supporting the finding."`
	Recommendation string `json:"recommendation" jsonschema:"description=Concrete remediation recommendation."`
}

type einoToolSet struct {
	mu             sync.Mutex
	executor       BlockExecutor
	journal        *evidence.Journal
	session        *sess.AgentSession
	authorizer     *authz.Authorizer
	scope          authz.Scope
	load           SkillLoader
	sleep          Sleeper
	networkBackoff time.Duration
	onEvent        func(RunnerEvent)
	action         int
	loaded         map[string]bool
	externalTools  []tool.BaseTool
}

const (
	execToolDesc          = "Run a Bash command in the engagement work directory. Use it for installed commands, pipelines, redirection, and short system operations."
	executePythonToolDesc = "Run a complete Python program with python3 -u in the engagement work directory. Use it for temporary request logic, parsing, batching, and custom analysis. Print observations to stdout."
	loadSkillToolDesc     = "Load one registered PentGo skill into the current engagement context."
	recordFindingToolDesc = "Record one evidence-backed finding. Every evidence_refs value must identify a successful action result. Recording does not end the engagement."
)

func (tools *einoToolSet) executeLanguage(ctx context.Context, toolName string, language exec.Language, source string, arguments any) (string, error) {
	startedAt := time.Now().UTC()
	tools.mu.Lock()
	tools.action++
	action := tools.action
	tools.mu.Unlock()
	block := exec.CodeBlock{Index: 1, Language: language, Code: source}
	preflight := exec.Preflight(block)
	if preflight.Approved && tools.authorizer != nil {
		decision := tools.authorizer.Authorize(preflight.Block, tools.scope)
		if !decision.Allowed {
			preflight.Approved, preflight.Rejection = false, decision.Reason
		}
	}
	if tools.onEvent != nil && preflight.Approved {
		tools.onEvent(RunnerEvent{Turn: action, Kind: "block_started", BlockIndex: 1, Detail: string(language)})
	}
	results := tools.executor.Execute(ctx, exec.ExecutionInput{SessionID: tools.session.ID, Target: tools.session.Target, Turn: action, Blocks: []exec.PreflightResult{preflight}})
	if len(results) == 0 {
		results = []exec.ExecutionResult{{Block: block, Status: exec.ExecutionFailed, ExitCode: -1, Error: "executor returned no result", StartedAt: startedAt, FinishedAt: time.Now().UTC()}}
	}
	result := results[0]
	if tools.onEvent != nil {
		tools.onEvent(RunnerEvent{Turn: action, Kind: "block_finished", BlockIndex: 1, Detail: string(result.Status)})
	}
	record, err := tools.journal.Record(toolName, arguments, result.Status == exec.ExecutionSucceeded, RenderExecutionResults(action, results), startedAt, result.FinishedAt)
	if err != nil {
		return "", err
	}
	if hasNetworkFriction(results) {
		if err := tools.sleep(ctx, tools.networkBackoff); err != nil {
			return "", err
		}
	}
	return record.Output, nil
}
func (tools *einoToolSet) exec(ctx context.Context, args execArgs) (string, error) {
	return tools.executeLanguage(ctx, "exec", exec.LanguageShell, args.Command, map[string]any{"command": args.Command})
}
func (tools *einoToolSet) executePython(ctx context.Context, args executePythonArgs) (string, error) {
	return tools.executeLanguage(ctx, "execute_python", exec.LanguagePython, args.Script, map[string]any{"script": args.Script})
}

func (tools *einoToolSet) recordFinding(_ context.Context, args recordFindingArgs) (string, error) {
	title, severity := strings.TrimSpace(args.Title), strings.ToLower(strings.TrimSpace(args.Severity))
	description, recommendation := strings.TrimSpace(args.Description), strings.TrimSpace(args.Recommendation)
	if title == "" {
		return "finding rejected: title is required", nil
	}
	if !map[string]bool{"critical": true, "high": true, "medium": true, "low": true, "info": true}[severity] {
		return "finding rejected: severity must be one of critical, high, medium, low, info", nil
	}
	if description == "" {
		return "finding rejected: description is required", nil
	}
	if recommendation == "" {
		return "finding rejected: recommendation is required", nil
	}
	if len(args.EvidenceRefs) == 0 {
		return "finding rejected: evidence_refs must contain at least one reference", nil
	}
	seen := make(map[int]bool, len(args.EvidenceRefs))
	for _, ref := range args.EvidenceRefs {
		if seen[ref] {
			return fmt.Sprintf("finding rejected: duplicate evidence_ref %d", ref), nil
		}
		seen[ref] = true
	}
	for _, ref := range args.EvidenceRefs {
		record, ok := tools.journal.Lookup(ref)
		if !ok {
			return fmt.Sprintf("finding rejected: evidence_ref %d does not exist", ref), nil
		}
		if !record.Success {
			return fmt.Sprintf("finding rejected: evidence_ref %d is not successful", ref), nil
		}
	}
	key := strings.ToLower(title)
	tools.mu.Lock()
	defer tools.mu.Unlock()
	for _, finding := range tools.session.Findings {
		if strings.ToLower(strings.TrimSpace(finding.Title)) == key {
			return "finding rejected: title already recorded", nil
		}
	}
	tools.session.Findings = append(tools.session.Findings, sess.Finding{Title: title, Severity: severity, Description: description, EvidenceRefs: append([]int(nil), args.EvidenceRefs...), Recommendation: recommendation})
	return fmt.Sprintf("finding #%d recorded", len(tools.session.Findings)), nil
}

func (tools *einoToolSet) loadSkill(_ context.Context, args loadSkillArgs) (string, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return "skill rejected: name is required", nil
	}
	tools.mu.Lock()
	defer tools.mu.Unlock()
	if tools.loaded[name] {
		return "skill " + name + " was already loaded", nil
	}
	content, err := tools.load(name)
	if err != nil {
		return "skill rejected: " + err.Error(), nil
	}
	tools.loaded[name] = true
	return "=== PENTGO SKILL CONTEXT ===\nskill: " + name + "\n" + content + "\n=== END PENTGO SKILL CONTEXT ===", nil
}

func (tools *einoToolSet) buildTools(ctx context.Context) ([]tool.BaseTool, error) {
	execTool, err := toolutils.InferTool("exec", execToolDesc, tools.exec)
	if err != nil {
		return nil, fmt.Errorf("infer exec tool: %w", err)
	}
	pythonTool, err := toolutils.InferTool("execute_python", executePythonToolDesc, tools.executePython)
	if err != nil {
		return nil, fmt.Errorf("infer execute_python tool: %w", err)
	}
	skillTool, err := toolutils.InferTool("load_skill", loadSkillToolDesc, tools.loadSkill)
	if err != nil {
		return nil, fmt.Errorf("infer load_skill tool: %w", err)
	}
	findingTool, err := toolutils.InferTool("record_finding", recordFindingToolDesc, tools.recordFinding)
	if err != nil {
		return nil, fmt.Errorf("infer record_finding tool: %w", err)
	}
	all := []tool.BaseTool{execTool, pythonTool, skillTool, findingTool}
	seen := map[string]bool{"exec": true, "execute_python": true, "load_skill": true, "record_finding": true}
	for _, external := range tools.externalTools {
		invokable, ok := external.(tool.InvokableTool)
		if external == nil || !ok || invokable == nil {
			return nil, fmt.Errorf("external tool must implement tool.InvokableTool")
		}
		info, err := external.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("inspect external tool: %w", err)
		}
		if info == nil {
			return nil, fmt.Errorf("external tool info is nil")
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			return nil, fmt.Errorf("external tool name is empty")
		}
		if seen[name] {
			return nil, fmt.Errorf("external tool name collision: %s", name)
		}
		seen[name] = true
		all = append(all, external)
	}
	return all, nil
}

func literalInstructionGenModelInput(_ context.Context, instruction string, input *adk.AgentInput) ([]adk.Message, error) {
	messages := make([]adk.Message, 0, len(input.Messages)+1)
	if instruction != "" {
		messages = append(messages, schema.SystemMessage(instruction))
	}
	return append(messages, input.Messages...), nil
}
func newEinoAgent(ctx context.Context, chatModel model.ToolCallingChatModel, instruction string, maxTurns int, tools *einoToolSet) (*adk.ChatModelAgent, error) {
	toolList, err := tools.buildTools(ctx)
	if err != nil {
		return nil, err
	}
	if maxTurns <= 0 {
		maxTurns = 20
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "pentgo-engagement", Description: "PentGo single-agent CTF engagement runtime.", Instruction: instruction, Model: chatModel, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList}}, GenModelInput: literalInstructionGenModelInput, MaxIterations: maxTurns})
}
