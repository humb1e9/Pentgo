package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/evidence"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func (runner *Runner) RunEino(ctx context.Context, session *sess.AgentSession, chatModel model.ToolCallingChatModel) error {
	if runner == nil || runner.executor == nil || runner.journal == nil || session == nil || chatModel == nil {
		return fmt.Errorf("eino runner dependencies are incomplete")
	}
	if session.Status != sess.SessionRunning {
		return fmt.Errorf("session must be running")
	}
	tools := &einoToolSet{executor: runner.executor, journal: runner.journal, session: session, authorizer: runner.config.Authorizer, scope: authz.NewScope(hostOf(session.Target), runner.config.AllowedHosts, runner.config.AllowPrivateHosts), load: runner.load, sleep: runner.sleep, networkBackoff: runner.config.NetworkBackoff, onEvent: runner.config.OnEvent, loaded: make(map[string]bool), externalTools: runner.config.MCPTools}
	agentImpl, err := newEinoAgent(ctx, chatModel, buildSystemPrompt(runner.catalog), runner.config.MaxTurns, tools)
	if err != nil {
		_ = session.Fail("agent_init_error", time.Now().UTC())
		return err
	}
	iterator := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agentImpl, EnableStreaming: false}).Run(ctx, []adk.Message{schema.UserMessage("TARGET: " + session.Target + "\nTASK: " + session.Intent)})
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			if errors.Is(event.Err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				_ = session.Cancel("cancelled", time.Now().UTC())
				return nil
			}
			if errors.Is(event.Err, evidence.ErrWrite) {
				_ = session.Fail("evidence_error", time.Now().UTC())
				return event.Err
			}
			if errors.Is(event.Err, adk.ErrExceedMaxIterations) {
				_ = session.Fail("max_iterations", time.Now().UTC())
				return nil
			}
			_ = session.Fail("provider_error", time.Now().UTC())
			return event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil || event.Output.MessageOutput.Role != schema.Assistant {
			continue
		}
		message, messageErr := event.Output.MessageOutput.GetMessage()
		if messageErr != nil || message == nil {
			continue
		}
		session.Turns++
		runner.emit(RunnerEvent{Turn: session.Turns, Kind: "assistant", Detail: assistantSummary(message.Content)})
		if len(message.ToolCalls) != 0 {
			continue
		}
		session.FinalSummary = strings.TrimSpace(message.Content)
		if session.FinalSummary == "" {
			_ = session.Fail("empty_response", time.Now().UTC())
		} else {
			_ = session.Complete("agent_finished", time.Now().UTC())
		}
		return nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		_ = session.Cancel("cancelled", time.Now().UTC())
		return nil
	}
	_ = session.Fail("max_iterations", time.Now().UTC())
	return nil
}
