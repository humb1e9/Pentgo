package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/domain"
)

// StepperFactory creates a stateless single-request model stepper for a turn.
type StepperFactory func(context.Context, *domain.Session, *ProjectRuntime) (agent.ModelStepper, error)

// EngineFactory is retained as the public construction seam while the host
// owns iteration, tool invocation, persistence, and retry behavior.
type EngineFactory = StepperFactory

type TurnService struct {
	configMu        sync.RWMutex
	stepper         agent.ModelStepper
	stepperFactory  StepperFactory
	store           *storage.ProjectStore
	tools           agent.ToolProvider
	loadSkill       SkillLoader
	skillsAvailable bool
	now             func() time.Time
	maxRequests     int
	systemPrompt    string
	assembler       ContextPreparer
}

func NewTurnService(stepper agent.ModelStepper, store *storage.ProjectStore, tools agent.ToolProvider) *TurnService {
	return &TurnService{stepper: stepper, store: store, tools: tools, now: func() time.Time { return time.Now().UTC() }, maxRequests: 20}
}

func (service *TurnService) SetEngineFactory(factory StepperFactory) {
	if service != nil {
		service.configMu.Lock()
		service.stepperFactory = factory
		service.configMu.Unlock()
	}
}
func (service *TurnService) SetSkillCatalog(load SkillLoader, available bool) {
	if service == nil {
		return
	}
	service.configMu.Lock()
	service.loadSkill = load
	service.skillsAvailable = available
	service.configMu.Unlock()
}
func (service *TurnService) SetClock(clock func() time.Time) {
	if service != nil && clock != nil {
		service.configMu.Lock()
		service.now = clock
		service.configMu.Unlock()
	}
}
func (service *TurnService) SetMaxRequests(max int) {
	if service != nil && max > 0 {
		service.configMu.Lock()
		service.maxRequests = max
		service.configMu.Unlock()
	}
}

// SetSystemPrompt installs the fixed provider system envelope used for every
// request and included in ContextAssembler preflight measurements.
func (service *TurnService) SetSystemPrompt(prompt string) {
	if service != nil {
		service.configMu.Lock()
		service.systemPrompt = strings.TrimSpace(prompt)
		service.configMu.Unlock()
	}
}

func (service *TurnService) SetContextAssembler(assembler ContextPreparer) {
	if service != nil {
		service.configMu.Lock()
		service.assembler = assembler
		service.configMu.Unlock()
	}
}

func (service *TurnService) RunTurn(ctx context.Context, runtime *ProjectRuntime, session *domain.Session, message string) error {
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
	transcript := runtime.Transcript(session.ID)
	if transcript == nil {
		return fmt.Errorf("session transcript is unavailable")
	}
	turn, err := session.BeginTurn("", message, service.currentTime())
	if err != nil {
		return err
	}
	factIndex := runtime.ProjectFactIndex()
	if factIndex == nil {
		return service.finishError(runtime, session, turn.ID, fmt.Errorf("project fact index is unavailable"), service.currentTime())
	}
	factSnapshot, err := factIndex.Snapshot(ctx)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	if targets := extractTargets(message); len(targets) != 0 {
		session.AddTargets(targets...)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: message}); err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	if err := service.persist(runtime, session); err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	runtime.PublishSnapshot(session.ID)
	runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnStarted, Message: message})
	stepper, err := service.resolveStepper(ctx, session, runtime)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	externalTools, err := service.resolveTools(ctx, runtime)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	loadSkill, skillsAvailable := service.skillConfig()
	tools, err := newRuntimeToolProvider(runtime, session, externalTools, loadSkill, skillsAvailable).Tools(ctx)
	if err != nil {
		return service.finishError(runtime, session, turn.ID, err, service.currentTime())
	}
	assembler := service.contextAssembler(runtime)
	systemPrompt := service.currentSystemPrompt()
	maxRequests := service.requestLimit()
	for request := 0; request < maxRequests; request++ {
		input, activities, err := assembler.Prepare(ctx, session.ID, systemPrompt, tools, factSnapshot)
		for _, activity := range activities {
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventContextActivity, Message: activity.Message, Data: activity})
		}
		if err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		var final *agent.Message
		overflowRecovered := false
		for {
			stream, stepErr := stepper.StreamStep(ctx, input)
			if stepErr != nil {
				if errors.Is(stepErr, agent.ErrContextWindowExceeded) && !overflowRecovered {
					overflowRecovered = true
					input, activities, stepErr = assembler.PrepareOverflowRecovery(ctx, session.ID, systemPrompt, tools, factSnapshot)
					for _, activity := range activities {
						runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventContextActivity, Message: activity.Message, Data: activity})
					}
					if stepErr == nil {
						continue
					}
				}
				if errors.Is(stepErr, agent.ErrContextWindowExceeded) && overflowRecovered {
					return service.finishError(runtime, session, turn.ID, stepErr, service.currentTime())
				}
				return service.finishError(runtime, session, turn.ID, stepErr, service.currentTime())
			}
			if stream == nil {
				return service.finishError(runtime, session, turn.ID, fmt.Errorf("model step returned nil stream"), service.currentTime())
			}
			final, err = consumeModelStream(ctx, runtime, session.ID, turn.ID, stream)
			if errors.Is(err, agent.ErrContextWindowExceeded) && !overflowRecovered {
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
			if err := transcript.Append(*final); err != nil {
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
			runtime.PublishSnapshot(session.ID)
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnFinished, Message: strings.TrimSpace(final.Content)})
			return nil
		}
		if err := validateToolCalls(*final, tools); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		if err := transcript.Append(*final); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		if err := service.persist(runtime, session); err != nil {
			return service.finishError(runtime, session, turn.ID, err, service.currentTime())
		}
		for _, call := range final.ToolCalls {
			runtime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventToolStarted, Message: call.Name, Data: call})
		}
		results := invokeToolCalls(ctx, runtime, tools, final.ToolCalls)
		toolMessages := make([]agent.Message, len(results))
		for index, result := range results {
			// A successful evidence write turns an invoked-tool failure into a
			// model-recoverable tool message. Any remaining error is an audit or
			// execution-boundary failure and must not create an unrecorded row.
			if result.err != nil {
				return service.finishError(runtime, session, turn.ID, result.err, service.currentTime())
			}
			toolMessages[index] = result.message
		}
		if err := transcript.AppendBatch(toolMessages); err != nil {
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

func consumeModelStream(ctx context.Context, runtime *ProjectRuntime, sessionID, turnID string, stream <-chan agent.ModelStreamEvent) (*agent.Message, error) {
	var final *agent.Message
	for event := range stream {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if final != nil {
			if event.Err != nil {
				return nil, fmt.Errorf("model step emitted error after final message: %w", event.Err)
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

type toolResult struct {
	message agent.Message
	err     error
}

func invokeToolCalls(ctx context.Context, runtime *ProjectRuntime, tools []agent.Tool, calls []agent.ToolCall) []toolResult {
	lookup := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		lookup[tool.Name()] = tool
	}
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	for index, call := range calls {
		index, call := index, call
		wg.Add(1)
		go func() {
			defer wg.Done()
			tool := lookup[call.Name]
			if tool == nil {
				results[index] = toolResult{message: agent.Message{Role: agent.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Content: "tool not found"}, err: fmt.Errorf("tool %q is not available", call.Name)}
				return
			}
			arguments := cloneToolArguments(call.Arguments)
			if strings.TrimSpace(call.RawArguments) != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(call.RawArguments), &parsed); err == nil {
					arguments = parsed
				}
			}
			output, invokeErr := tool.Invoke(ctx, arguments)
			decorated, recordErr := recordToolResult(runtime, call.Name, arguments, output, invokeErr)
			if recordErr != nil {
				results[index] = toolResult{message: agent.Message{Role: agent.RoleTool, ToolCallID: call.ID, ToolName: call.Name, ToolArguments: arguments, Content: decorated}, err: recordErr}
				return
			}
			results[index] = toolResult{message: agent.Message{Role: agent.RoleTool, ToolCallID: call.ID, ToolName: call.Name, ToolArguments: arguments, Content: decorated}, err: nil}
		}()
	}
	wg.Wait()
	return results
}

func recordToolResult(runtime *ProjectRuntime, name string, arguments map[string]any, output string, invokeErr error) (string, error) {
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

func cloneToolArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = cloneToolValue(value)
	}
	return cloned
}

func cloneToolValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneToolArguments(typed)
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = cloneToolValue(item)
		}
		return items
	default:
		return value
	}
}
func validateToolCalls(message agent.Message, tools []agent.Tool) error {
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

func (service *TurnService) resolveStepper(ctx context.Context, session *domain.Session, runtime *ProjectRuntime) (agent.ModelStepper, error) {
	service.configMu.RLock()
	stepper, factory := service.stepper, service.stepperFactory
	service.configMu.RUnlock()
	if stepper != nil {
		return stepper, nil
	}
	if factory == nil {
		return nil, fmt.Errorf("model stepper is not configured")
	}
	return factory(ctx, session, runtime)
}
func (service *TurnService) resolveTools(ctx context.Context, runtime *ProjectRuntime) ([]agent.Tool, error) {
	if service.tools != nil {
		return service.tools.Tools(ctx)
	}
	return runtime.Tools(ctx)
}
func (service *TurnService) contextAssembler(runtime *ProjectRuntime) ContextPreparer {
	service.configMu.RLock()
	configured := service.assembler
	service.configMu.RUnlock()
	if configured != nil {
		return configured
	}
	return NewContextAssembler(runtime, config.AgentContextConfig{}, NewContextMeter(), nil)
}
func (service *TurnService) currentSystemPrompt() string {
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	return service.systemPrompt
}

func (service *TurnService) requestLimit() int {
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	if service.maxRequests > 0 {
		return service.maxRequests
	}
	return 20
}
func (service *TurnService) persist(runtime *ProjectRuntime, session *domain.Session) error {
	if runtime.Store() == nil && service.store != nil {
		return service.store.SaveSession(session)
	}
	return runtime.PersistState(session)
}
func (service *TurnService) finishError(runtime *ProjectRuntime, session *domain.Session, turnID string, turnErr error, finishedAt time.Time) error {
	if session.ActiveTurn != nil && session.ActiveTurn.ID == turnID && session.ActiveTurn.Status == domain.TurnRunning {
		if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
			_ = session.InterruptTurn(turnID, turnErr.Error(), finishedAt)
		} else {
			_ = session.FailTurn(turnID, turnErr.Error(), finishedAt)
		}
	}
	if persistErr := service.persist(runtime, session); persistErr != nil {
		return fmt.Errorf("%v; persist turn: %w", turnErr, persistErr)
	}
	runtime.PublishSnapshot(session.ID)
	if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
		runtime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFinished, Message: "turn interrupted"})
	} else {
		runtime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFailed, Message: turnErr.Error()})
	}
	return turnErr
}
func (service *TurnService) currentTime() time.Time {
	service.configMu.RLock()
	clock := service.now
	service.configMu.RUnlock()
	if clock == nil {
		return time.Now().UTC()
	}
	return clock().UTC()
}
func (service *TurnService) skillConfig() (SkillLoader, bool) {
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	return service.loadSkill, service.skillsAvailable
}
