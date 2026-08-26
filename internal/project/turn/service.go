package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/core"
	projectcontext "pentgo/internal/project/context"
	sessionstate "pentgo/internal/project/session"
)

// StepperFactory creates a stateless single-request model stepper for a turn.
type StepperFactory func(context.Context, *sessionstate.Session, Runtime) (core.ModelStepper, error)

// TurnServiceConfig contains fixed dependencies and limits for one service.
type TurnServiceConfig struct {
	StepperFactory StepperFactory
	BuildTools     ToolBuilder
	Clock          func() time.Time
	MaxRequests    int
	SystemPrompt   string
	SkillContext   func(string) string
	Assembler      projectcontext.ContextPreparer
}

type TurnService struct {
	stepper        core.ModelStepper
	stepperFactory StepperFactory
	tools          core.ToolProvider
	buildTools     ToolBuilder
	now            func() time.Time
	maxRequests    int
	systemPrompt   string
	skillContext   func(string) string
	assembler      projectcontext.ContextPreparer
}

func NewTurnService(stepper core.ModelStepper, tools core.ToolProvider, cfg TurnServiceConfig) *TurnService {
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	if cfg.MaxRequests <= 0 {
		cfg.MaxRequests = 1000
	}
	return &TurnService{
		stepper:        stepper,
		stepperFactory: cfg.StepperFactory,
		tools:          tools,
		buildTools:     cfg.BuildTools,
		now:            cfg.Clock,
		maxRequests:    cfg.MaxRequests,
		systemPrompt:   strings.TrimSpace(cfg.SystemPrompt),
		skillContext:   cfg.SkillContext,
		assembler:      cfg.Assembler,
	}
}

func (service *TurnService) RunTurn(ctx context.Context, runtime Runtime, session *sessionstate.Session, message string) error {
	if service == nil || runtime == nil || session == nil {
		return fmt.Errorf("turn service dependencies are incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("user message is empty")
	}
	if session.Name == "新会话" && session.Turns == 0 {
		_ = session.Rename(message)
	}
	conversation := runtime.Conversation(session.ID)
	if conversation == nil {
		return fmt.Errorf("session conversation is unavailable")
	}
	turn, err := session.BeginTurn("", message, service.currentTime())
	if err != nil {
		return err
	}
	factSnapshot, err := runtime.FactSnapshot(ctx)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	if targets := extractTargets(message); len(targets) != 0 {
		session.AddTargets(targets...)
	}
	if err := conversation.Append(core.Message{Role: core.RoleUser, Content: message}); err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	if err := service.persist(runtime, session); err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	runtime.Publish(session.ID)
	runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnStarted, Message: message})
	stepper, err := service.resolveStepper(ctx, session, runtime)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	externalTools, err := service.resolveTools(ctx, runtime)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	tools, err := service.resolveBuiltTools(ctx, runtime, session, externalTools)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	assembler := service.contextAssembler(runtime)
	systemPrompt := service.systemPrompt
	if service.skillContext != nil {
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + service.skillContext(message))
	}
	maxRequests := service.maxRequests
	for request := 0; request < maxRequests; request++ {
		input, activities, err := assembler.Prepare(ctx, session.ID, systemPrompt, tools, factSnapshot)
		for _, activity := range activities {
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventContextActivity, Message: activity.Message, Data: activity})
		}
		if err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		var final *core.Message
		overflowRecovered := false
		for {
			stream, stepErr := stepper.StreamStep(ctx, input)
			if stepErr != nil {
				if errors.Is(stepErr, core.ErrContextWindowExceeded) && !overflowRecovered {
					overflowRecovered = true
					input, activities, stepErr = assembler.PrepareOverflowRecovery(ctx, session.ID, systemPrompt, tools, factSnapshot)
					for _, activity := range activities {
						runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventContextActivity, Message: activity.Message, Data: activity})
					}
					if stepErr == nil {
						continue
					}
				}
				if errors.Is(stepErr, core.ErrContextWindowExceeded) && overflowRecovered {
					return service.finishError(runtime, session, turn.ID, stepErr, service.currentTime())
				}
				return service.finishError(runtime, session, turn.ID, stepErr, service.currentTime())
			}
			if stream == nil {
				return service.finishError(runtime, session, turn.ID, fmt.Errorf("model step returned nil stream"), service.currentTime())
			}
			final, err = consumeModelStream(ctx, runtime, session.ID, turn.ID, stream)
			if errors.Is(err, core.ErrContextWindowExceeded) && !overflowRecovered {
				overflowRecovered = true
				input, activities, err = assembler.PrepareOverflowRecovery(ctx, session.ID, systemPrompt, tools, factSnapshot)
				for _, activity := range activities {
					runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventContextActivity, Message: activity.Message, Data: activity})
				}
				if err == nil {
					continue
				}
			}
			if err != nil {
				return service.finishError(runtime, session, turn.ID, err, service.currentTime())
			}
			break
		}
		if final == nil {
			return service.finishError(runtime, session, turn.ID, fmt.Errorf("model step ended without final message"), service.currentTime())
		}
		if len(final.ToolCalls) == 0 {
			if strings.TrimSpace(final.Content) == "" {
				return service.finishError(runtime, session, turn.ID, fmt.Errorf("model step ended without final assistant text"), service.currentTime())
			}
			if err := conversation.Append(*final); err != nil {
				return service.finishError(runtime, session, turn.ID, err, service.currentTime())
			}
			if err := service.persist(runtime, session); err != nil {
				return service.finishError(runtime, session, turn.ID, err, service.currentTime())
			}
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventAssistantMessage, Message: strings.TrimSpace(final.Content)})
			if err := session.FinishTurn(turn.ID, strings.TrimSpace(final.Content), service.currentTime()); err != nil {
				return err
			}
			if err := service.persist(runtime, session); err != nil {
				return err
			}
			runtime.Publish(session.ID)
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnFinished, Message: strings.TrimSpace(final.Content)})
			return nil
		}
		if err := validateToolCalls(*final, tools); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		if err := conversation.Append(*final); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		if err := service.persist(runtime, session); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		for _, call := range final.ToolCalls {
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventToolStarted, Message: call.Name, Data: call})
		}
		results := invokeToolCalls(ctx, runtime, tools, final.ToolCalls)
		toolMessages := make([]core.Message, len(results))
		for index, result := range results {
			// A successful evidence write turns an invoked-tool failure into a
			// model-recoverable tool message. Any remaining error is an audit or
			// execution-boundary failure and must not create an unrecorded row.
			if result.Err != nil {
				return service.finishError(runtime, session, turn.ID, result.Err, service.currentTime())
			}
			toolMessages[index] = result.Message
		}
		if err := conversation.AppendBatch(toolMessages); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		if err := service.persist(runtime, session); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		for _, message := range toolMessages {
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventToolFinished, Message: message.ToolName, Output: message.Content})
		}
	}
	return service.finishError(runtime, session, turn.ID, fmt.Errorf("maximum model requests exceeded"), service.currentTime())
}

func consumeModelStream(ctx context.Context, runtime Runtime, sessionID, turnID string, stream <-chan core.ModelStreamEvent) (*core.Message, error) {
	var final *core.Message
	for event := range stream {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if final != nil {
			if event.Err != nil {
				return nil, fmt.Errorf("model step emitted error after final Message: %w", event.Err)
			}
			if event.Final != nil {
				return nil, fmt.Errorf("model step emitted multiple final messages")
			}
			if event.Delta.Role != "" || event.Delta.Content != "" || len(event.Delta.ToolCalls) != 0 {
				return nil, fmt.Errorf("model step emitted output after final message")
			}
			continue
		}
		if event.Err != nil {
			return nil, event.Err
		}
		if event.Delta.Role != "" || event.Delta.Content != "" || len(event.Delta.ToolCalls) != 0 {
			runtime.Emit(sessionID, Event{TurnID: turnID, Kind: EventAssistantDelta, Message: event.Delta.Content, Data: event.Delta})
		}
		if event.Final != nil {
			copy := *event.Final
			final = &copy
		}
	}
	return final, nil
}

type ToolResult struct {
	Message core.Message
	Err     error
}

func invokeToolCalls(ctx context.Context, runtime Runtime, tools []core.Tool, calls []core.ToolCall) []ToolResult {
	lookup := make(map[string]core.Tool, len(tools))
	for _, tool := range tools {
		lookup[tool.Name()] = tool
	}
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for index, call := range calls {
		index, call := index, call
		wg.Add(1)
		go func() {
			defer wg.Done()
			tool := lookup[call.Name]
			if tool == nil {
				results[index] = ToolResult{Message: core.Message{Role: core.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Content: "tool not found"}, Err: fmt.Errorf("tool %q is not available", call.Name)}
				return
			}
			arguments := core.CloneArguments(call.Arguments)
			if strings.TrimSpace(call.RawArguments) != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(call.RawArguments), &parsed); err == nil {
					arguments = parsed
				}
			}
			output, invokeErr := tool.Invoke(ctx, arguments)
			decorated, recordErr := recordToolResult(runtime, call.Name, arguments, output, invokeErr)
			if recordErr != nil {
				results[index] = ToolResult{Message: core.Message{Role: core.RoleTool, ToolCallID: call.ID, ToolName: call.Name, ToolArguments: arguments, Content: decorated}, Err: recordErr}
				return
			}
			results[index] = ToolResult{Message: core.Message{Role: core.RoleTool, ToolCallID: call.ID, ToolName: call.Name, ToolArguments: arguments, Content: decorated}, Err: nil}
		}()
	}
	wg.Wait()
	return results
}

func recordToolResult(runtime Runtime, name string, arguments map[string]any, output string, invokeErr error) (string, error) {
	if invokeErr != nil {
		if strings.TrimSpace(output) == "" {
			output = "工具调用失败：" + invokeErr.Error()
		} else {
			output = "工具调用失败：" + invokeErr.Error() + "\n" + output
		}
	}
	if runtime == nil || runtime.Evidence() == nil {
		return output, invokeErr
	}
	record, err := runtime.Evidence().RecordResult(context.Background(), name, arguments, invokeErr == nil, output)
	if err != nil {
		return "", err
	}
	return record.Output, nil
}

func validateToolCalls(message core.Message, tools []core.Tool) error {
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	ids := make(map[string]bool, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if strings.TrimSpace(call.ID) == "" || ids[call.ID] {
			return fmt.Errorf("invalid model tool call id: %s", call.ID)
		}
		if strings.TrimSpace(call.Name) == "" || !names[call.Name] {
			return fmt.Errorf("invalid model tool call: %s", call.Name)
		}
		if strings.TrimSpace(call.RawArguments) != "" {
			var object map[string]any
			if err := json.Unmarshal([]byte(call.RawArguments), &object); err != nil || object == nil {
				return fmt.Errorf("invalid arguments for tool %s", call.Name)
			}
		}
		ids[call.ID] = true
	}
	return nil
}

func (service *TurnService) resolveStepper(ctx context.Context, session *sessionstate.Session, runtime Runtime) (core.ModelStepper, error) {
	if service.stepper != nil {
		return service.stepper, nil
	}
	if service.stepperFactory == nil {
		return nil, fmt.Errorf("model stepper is not configured")
	}
	return service.stepperFactory(ctx, session, runtime)
}
func (service *TurnService) resolveTools(ctx context.Context, runtime Runtime) ([]core.Tool, error) {
	if service.tools != nil {
		return service.tools.Tools(ctx)
	}
	return runtime.Tools(ctx)
}
func (service *TurnService) resolveBuiltTools(ctx context.Context, runtime Runtime, session *sessionstate.Session, external []core.Tool) ([]core.Tool, error) {
	if service.buildTools == nil {
		return external, nil
	}
	return service.buildTools(ctx, runtime, session, external)
}
func (service *TurnService) contextAssembler(runtime Runtime) projectcontext.ContextPreparer {
	if service.assembler != nil {
		return service.assembler
	}
	return runtime.ContextPreparer()
}
func (service *TurnService) persist(runtime Runtime, session *sessionstate.Session) error {
	return runtime.Persist(session)
}
func (service *TurnService) finishError(runtime Runtime, session *sessionstate.Session, turnID string, turnErr error, finishedAt time.Time) error {
	if session.ActiveTurn != nil && session.ActiveTurn.ID == turnID && session.ActiveTurn.Status == sessionstate.TurnRunning {
		if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
			_ = session.InterruptTurn(turnID, turnErr.Error(), finishedAt)
		} else {
			_ = session.FailTurn(turnID, turnErr.Error(), finishedAt)
		}
	}
	if persistErr := service.persist(runtime, session); persistErr != nil {
		return fmt.Errorf("%v; persist turn: %w", turnErr, persistErr)
	}
	runtime.Publish(session.ID)
	if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
		runtime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFinished, Message: "turn interrupted"})
	} else {
		runtime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFailed, Message: turnErr.Error()})
	}
	return turnErr
}
func (service *TurnService) currentTime() time.Time {
	return service.now().UTC()
}

func ConsumeModelStream(ctx context.Context, runtime Runtime, sessionID, turnID string, stream <-chan core.ModelStreamEvent) (*core.Message, error) {
	return consumeModelStream(ctx, runtime, sessionID, turnID, stream)
}
func InvokeToolCalls(ctx context.Context, runtime Runtime, tools []core.Tool, calls []core.ToolCall) []ToolResult {
	return invokeToolCalls(ctx, runtime, tools, calls)
}
