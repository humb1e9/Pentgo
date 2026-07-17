package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	"pentgo/internal/agent"
	"pentgo/skills"
)

var skillLoadPattern = regexp.MustCompile(`(?m)^SKILL_LOAD:\s*([a-z][a-z0-9_-]*)\s*$`)

// BlockExecutor 是 Runner 所依赖的本地代码执行边界。
type BlockExecutor interface {
	Execute(context.Context, ExecutionInput) []ExecutionResult
}

// SkillLoader 读取一个已注册的只读 Skill。
type SkillLoader func(string) (string, error)

// Sleeper 使恢复等待可被测试替换和任务取消中断。
type Sleeper func(context.Context, time.Duration) error

// RunnerConfig 约束模型循环和恢复策略。
type RunnerConfig struct {
	MaxTurns           int
	NoCodeLimit        int
	ProviderRetryDelay time.Duration
	NetworkBackoff     time.Duration
	SoftStuckTurns     int
	HardStuckTurns     int
	OnEvent            func(RunnerEvent)
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
	client      agent.Client
	executor    BlockExecutor
	config      RunnerConfig
	load        SkillLoader
	sleep       Sleeper
	reportTurns []ReportTurn
}

// NewRunner 创建一个模型循环。nil loader 和 sleeper 使用默认实现。
func NewRunner(client agent.Client, executor BlockExecutor, config RunnerConfig, load SkillLoader, sleep Sleeper) *Runner {
	if config.MaxTurns <= 0 {
		config.MaxTurns = 20
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
	if load == nil {
		load = skills.Load
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &Runner{client: client, executor: executor, config: config, load: load, sleep: sleep}
}

// Run 持续请求模型、执行全部代码块并把实际结果回灌，直到会话到达终态。
func (runner *Runner) Run(ctx context.Context, session *AgentSession) error {
	if runner == nil || runner.client == nil || runner.executor == nil || session == nil {
		return fmt.Errorf("runner dependencies are incomplete")
	}
	if err := session.Start(time.Now().UTC()); err != nil {
		return err
	}
	runner.reportTurns = nil
	history := NewHistory(session.Target.Canonical, session.Intent)
	loadedSkills := make(map[string]bool)
	noCodeCount := 0
	hasExecutionEvidence := false
	lastFingerprint := ""
	fingerprintCount := 0

	for session.Turn < runner.config.MaxTurns {
		if err := ctx.Err(); err != nil {
			_ = session.Cancel("cancelled", time.Now().UTC())
			return nil
		}
		response, err := runner.chat(ctx, agent.Request{SystemPrompt: systemPrompt, Messages: history.Messages()})
		if err != nil {
			_ = session.Fail("provider_error", time.Now().UTC())
			session.AddEvent(session.Turn, "provider_error", err.Error(), time.Now().UTC())
			return err
		}
		session.Turn++
		turn := session.Turn
		assistantText := strings.TrimSpace(response.Content)
		history.Append("assistant", assistantText)
		session.AddEvent(turn, "assistant", "model response received", time.Now().UTC())
		runner.emit(RunnerEvent{Turn: turn, Kind: "assistant", Detail: assistantSummary(assistantText)})
		runner.reportTurns = append(runner.reportTurns, ReportTurn{Number: turn, Decision: assistantSummary(assistantText)})

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
			history.Append("user", "STRATEGY CHANGE REQUIRED: the recent assistant responses repeat the same plan. Generate materially different executable evidence collection code.")
			session.AddEvent(turn, "recovery", "strategy_change_required", time.Now().UTC())
		}

		skillHandled := runner.loadSkills(assistantText, loadedSkills, history, session, turn)
		blocks := ExtractCodeBlocks(assistantText)
		if len(blocks) == 0 {
			if isCompletion(assistantText) {
				if !hasExecutionEvidence {
					history.Append("user", "EVIDENCE REQUIRED: completion requires at least one returned code execution result. Generate executable code and print evidence.")
					session.AddEvent(turn, "recovery", "evidence_required", time.Now().UTC())
					continue
				}
				reason := completionReason(assistantText)
				_ = session.Complete(reason, time.Now().UTC())
				return nil
			}
			if skillHandled {
				continue
			}
			noCodeCount++
			instruction := "CONTINUE REQUIRED: generate executable Python or Bash code and print actual evidence."
			if makesClaim(assistantText) {
				instruction = "EVIDENCE REQUIRED: do not claim findings or completion without executable code and returned output."
			}
			history.Append("user", instruction)
			session.AddEvent(turn, "recovery", strings.Split(instruction, ":")[0], time.Now().UTC())
			if noCodeCount >= runner.config.NoCodeLimit {
				_ = session.Complete("no_executable_response", time.Now().UTC())
				return nil
			}
			continue
		}
		noCodeCount = 0

		preflight := make([]PreflightResult, 0, len(blocks))
		approved := make([]PreflightResult, 0, len(blocks))
		for _, block := range blocks {
			result := Preflight(block)
			preflight = append(preflight, result)
			if result.Approved {
				approved = append(approved, result)
			}
		}
		if len(approved) == 0 {
			results := runner.executor.Execute(ctx, ExecutionInput{
				SessionID: session.ID,
				Target:    session.Target.Canonical,
				Turn:      turn,
				Blocks:    preflight,
			})
			runner.recordReportBlocks(results)
			history.Append("user", renderPreflightRejections(turn, preflight))
			session.AddEvent(turn, "recovery", "preflight_rejected", time.Now().UTC())
			continue
		}

		for _, block := range approved {
			runner.emit(RunnerEvent{Turn: turn, Kind: "block_started", BlockIndex: block.Block.Index, Detail: string(block.Block.Language)})
		}
		results := runner.executor.Execute(ctx, ExecutionInput{
			SessionID: session.ID,
			Target:    session.Target.Canonical,
			Turn:      turn,
			Blocks:    preflight,
		})
		if len(approved) > 0 {
			hasExecutionEvidence = true
		}
		for _, result := range results {
			runner.emit(RunnerEvent{Turn: turn, Kind: "block_finished", BlockIndex: result.Block.Index, Detail: string(result.Status)})
		}
		runner.recordReportBlocks(results)
		resultText := RenderExecutionResults(turn, results)
		history.Append("user", resultText)
		session.AddEvent(turn, "execution", fmt.Sprintf("%d block(s)", len(results)), time.Now().UTC())
		if allNoOutput(results) {
			history.Append("user", "SCRIPT NO OUTPUT: all executed blocks produced no stdout or stderr. Rewrite the code to perform an operation and print evidence.")
			session.AddEvent(turn, "recovery", "script_no_output", time.Now().UTC())
		}
		if hasNetworkFriction(results) {
			if err := runner.sleep(ctx, runner.config.NetworkBackoff); err != nil {
				_ = session.Cancel("cancelled", time.Now().UTC())
				return nil
			}
			history.Append("user", "NETWORK FRICTION: execution output indicates throttling or transport failure. Adjust rate, target, or strategy before continuing.")
			session.AddEvent(turn, "recovery", "network_friction", time.Now().UTC())
		}
	}
	_ = session.Fail("max_turns", time.Now().UTC())
	return nil
}

// ReportContext 返回本次 Runner 执行收集的有界、无代码报告上下文副本。
func (runner *Runner) ReportContext(session *AgentSession) ReportContext {
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
	context.Turns = make([]ReportTurn, len(runner.reportTurns))
	for index, turn := range runner.reportTurns {
		context.Turns[index] = ReportTurn{Number: turn.Number, Decision: turn.Decision, Blocks: append([]ReportBlock(nil), turn.Blocks...)}
	}
	return context
}

func (runner *Runner) recordReportBlocks(results []ExecutionResult) {
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

func (runner *Runner) loadSkills(text string, loaded map[string]bool, history *History, session *AgentSession, turn int) bool {
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
func RenderExecutionResults(turn int, results []ExecutionResult) string {
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

func renderPreflightRejections(turn int, results []PreflightResult) string {
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

func allNoOutput(results []ExecutionResult) bool {
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

func hasNetworkFriction(results []ExecutionResult) bool {
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
	if len(ExtractCodeBlocks(text)) > 0 {
		return "code blocks received"
	}
	return "response received"
}
