package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pentgo/internal/runtime/authz"

	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// RunEino drives the openai engagement path on the Eino ADK. It mirrors the
// hand-rolled (*Runner).Run lifecycle and populates the SAME runner accumulators
// (history, reportTurns, findings, sessionPool) so ConsolidateAndVerify and
// ReportContext work unchanged. The provider branch in engagement.go chooses
// between Run (anthropic, text protocol) and RunEino (openai, native tool-call).
//
// The cross-turn control flow the runner did inline (stuck sha256, refusal count,
// no-tool-call count) lives here in the event-consumer loop because ADK's
// AsyncIterator delivers events concurrently with tool execution; the loop is the
// sole ordered writer of history/reportTurns and pops the tool set's FIFO stashes
// in source order.
func (runner *Runner) RunEino(ctx context.Context, session *sess.AgentSession, chatModel model.ToolCallingChatModel) error {
	if runner == nil || runner.executor == nil || session == nil {
		return fmt.Errorf("eino runner dependencies are incomplete")
	}
	if chatModel == nil {
		return fmt.Errorf("eino runner requires a chat model")
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

	establisher, _ := runner.config.Verifier.(sessionEstablisher)
	tools := &einoToolSet{
		executor:       runner.executor,
		authorizer:     runner.config.Authorizer,
		scope:          authz.NewScope(hostOf(session.Target.Canonical), runner.config.AllowedHosts, runner.config.AllowPrivateHosts),
		sessionID:      session.ID,
		target:         session.Target.Canonical,
		load:           runner.load,
		establisher:    establisher,
		sessionPool:    runner.sessionPool,
		sleep:          runner.sleep,
		networkBackoff: runner.config.NetworkBackoff,
		onBlockEvent:   runner.config.OnEvent,
	}

	agentImpl, err := newEinoAgent(ctx, chatModel, buildOpenAISystemPrompt(runner.catalog), runner.config.MaxTurns, tools)
	if err != nil {
		_ = session.Fail("agent_init_error", time.Now().UTC())
		session.AddEvent(session.Turn, "provider_error", err.Error(), time.Now().UTC())
		return err
	}

	adkRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agentImpl, EnableStreaming: false})
	task := "TARGET: " + session.Target.Canonical + "\nTASK: " + session.Intent
	iterator := adkRunner.Run(ctx, []adk.Message{schema.UserMessage(task)})

	lastFingerprint := ""
	fingerprintCount := 0
	refusalCount := 0
	noToolCallCount := 0

	for {
		if err := ctx.Err(); err != nil {
			_ = session.Cancel("cancelled", time.Now().UTC())
			return nil
		}
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			_ = session.Fail("provider_error", time.Now().UTC())
			session.AddEvent(session.Turn, "provider_error", event.Err.Error(), time.Now().UTC())
			return event.Err
		}
		if event.Action != nil && event.Action.Exit {
			if session.Status == sess.SessionRunning {
				_ = session.Complete("task_complete", time.Now().UTC())
			}
			return nil
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, err := event.Output.MessageOutput.GetMessage()
		if err != nil || message == nil {
			continue
		}

		switch event.Output.MessageOutput.Role {
		case schema.Assistant:
			stop := runner.consumeEinoAssistant(session, message, &lastFingerprint, &fingerprintCount, &refusalCount, &noToolCallCount)
			if stop {
				return nil
			}
		case schema.Tool:
			runner.consumeEinoToolResult(session, tools, message, event.Output.MessageOutput.ToolName)
		}
	}

	if session.Status == sess.SessionRunning {
		_ = session.Fail("max_turns", time.Now().UTC())
	}
	return nil
}

// consumeEinoAssistant records an assistant turn and applies the cross-turn
// stuck/refusal/no-tool-call guards. It returns true when the session reached a
// terminal state and the loop must stop.
func (runner *Runner) consumeEinoAssistant(session *sess.AgentSession, message *schema.Message, lastFingerprint *string, fingerprintCount, refusalCount, noToolCallCount *int) bool {
	session.Turn++
	turn := session.Turn
	assistantText := strings.TrimSpace(message.Content)
	runner.history.Append("assistant", einoAssistantHistory(message))
	session.AddEvent(turn, "assistant", "model response received", time.Now().UTC())
	runner.emit(RunnerEvent{Turn: turn, Kind: "assistant", Detail: assistantSummary(assistantText)})
	runner.reportTurns = append(runner.reportTurns, ReportTurn{Number: turn, Decision: assistantSummary(assistantText), DeclaredLabels: extractFindingLabels(assistantText)})

	fingerprint := responseFingerprint(einoFingerprintSource(message))
	if fingerprint == *lastFingerprint {
		*fingerprintCount++
	} else {
		*lastFingerprint = fingerprint
		*fingerprintCount = 1
	}
	if runner.config.HardStuckTurns > 0 && *fingerprintCount >= runner.config.HardStuckTurns {
		_ = session.Fail("stuck", time.Now().UTC())
		session.AddEvent(turn, "recovery", "stuck", time.Now().UTC())
		return true
	}
	if runner.config.SoftStuckTurns > 0 && *fingerprintCount >= runner.config.SoftStuckTurns {
		session.AddEvent(turn, "recovery", "strategy_change_required", time.Now().UTC())
	}

	if len(message.ToolCalls) > 0 {
		*noToolCallCount = 0
		return false
	}

	// No tool call: the assistant produced only text this turn.
	if isRefusal(assistantText) {
		*refusalCount++
		session.AddEvent(turn, "recovery", "refusal_recovery", time.Now().UTC())
		if runner.config.RefusalLimit > 0 && *refusalCount >= runner.config.RefusalLimit {
			_ = session.Fail("refused", time.Now().UTC())
			return true
		}
		return false
	}
	*noToolCallCount++
	instruction := "no_executable_tool_call"
	if makesClaim(assistantText) {
		instruction = "evidence_required"
	}
	session.AddEvent(turn, "recovery", instruction, time.Now().UTC())
	if runner.config.NoCodeLimit > 0 && *noToolCallCount >= runner.config.NoCodeLimit {
		_ = session.Complete("no_executable_response", time.Now().UTC())
		return true
	}
	return false
}

// consumeEinoToolResult folds a tool-result event back into runner state. For
// execute_code it pops the structured redacted results the tool stashed and
// records them as report blocks on the current turn; for declare_session it
// records the session pool's public view and the render text into history.
func (runner *Runner) consumeEinoToolResult(session *sess.AgentSession, tools *einoToolSet, message *schema.Message, toolName string) {
	switch toolName {
	case "execute_code":
		if results, ok := tools.popResults(); ok {
			runner.recordReportBlocks(results)
			session.AddEvent(session.Turn, "execution", fmt.Sprintf("%d block(s)", len(results)), time.Now().UTC())
		}
		runner.history.Append("user", strings.TrimSpace(message.Content))
	case "declare_session":
		if _, ok := tools.popSessionResult(); ok {
			session.Sessions = tools.sessionPool.PublicView()
			session.AddEvent(session.Turn, "session_declared", strings.TrimSpace(message.Content), time.Now().UTC())
		}
		runner.history.Append("user", strings.TrimSpace(message.Content))
	default:
		runner.history.Append("user", strings.TrimSpace(message.Content))
	}
}

// einoAssistantHistory renders an assistant message for the text history used by
// ConsolidateAndVerify: content plus a compact note of any tool calls so the
// consolidation model can see what code was run even without the tool results.
func einoAssistantHistory(message *schema.Message) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(message.Content))
	for _, call := range message.ToolCalls {
		if call.Function.Name == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(fmt.Sprintf("[tool_call %s] %s", call.Function.Name, call.Function.Arguments))
	}
	return builder.String()
}

// einoFingerprintSource builds the stuck-detection fingerprint from both the
// assistant text and its tool-call arguments, so repeating the identical
// execute_code call counts as stuck even when the text content is empty.
func einoFingerprintSource(message *schema.Message) string {
	var builder strings.Builder
	builder.WriteString(message.Content)
	for _, call := range message.ToolCalls {
		builder.WriteString("\n")
		builder.WriteString(call.Function.Name)
		builder.WriteString(call.Function.Arguments)
	}
	return builder.String()
}
