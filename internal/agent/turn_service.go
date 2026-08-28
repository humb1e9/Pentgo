package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/core"
	llm "pentgo/internal/model"
	projectmodel "pentgo/internal/project"
	projectturn "pentgo/internal/project/turn"
	sessionstate "pentgo/internal/session"
	"pentgo/internal/storage"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
)

// TurnService runs a project turn while the runtime keeps ownership of the
// durable conversation, evidence, and session lifecycle.
type TurnService struct {
	newModel      func(context.Context) (model.ToolCallingChatModel, error)
	skillContext  func(string) string
	now           func() time.Time
	maxRequests   int
	systemPrompt  string
	contextConfig projectmodel.ContextConfig
	summarizer    SummaryWriter
	mu            sync.Mutex
	cancellations map[string]adk.AgentCancelFunc
}

type TurnServiceConfig struct {
	NewModel     func(context.Context) (model.ToolCallingChatModel, error)
	SkillContext func(string) string
	Clock        func() time.Time
	MaxRequests  int
	SystemPrompt string
	Context      projectmodel.ContextConfig
	Summarizer   SummaryWriter
}

func NewTurnService(cfg TurnServiceConfig) *TurnService {
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &TurnService{
		newModel:      cfg.NewModel,
		skillContext:  cfg.SkillContext,
		now:           cfg.Clock,
		maxRequests:   cfg.MaxRequests,
		systemPrompt:  strings.TrimSpace(cfg.SystemPrompt),
		contextConfig: projectmodel.Config{Context: cfg.Context}.Effective().Context,
		summarizer:    cfg.Summarizer,
		cancellations: make(map[string]adk.AgentCancelFunc),
	}
}

func (service *TurnService) RunTurn(ctx context.Context, runtime *ProjectRuntime, session *sessionstate.Session, message string) error {
	if service == nil || service.newModel == nil || runtime == nil || session == nil {
		return fmt.Errorf("runner turn service dependencies are incomplete")
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
	resuming := sessionstate.ResumeTurnRequested(ctx)
	var turn *sessionstate.Turn
	var err error
	if resuming {
		if session.ActiveTurn == nil || session.ActiveTurn.Status != sessionstate.TurnRunning {
			return fmt.Errorf("runner turn is not ready to resume")
		}
		turn = session.ActiveTurn
	} else {
		turn, err = session.BeginTurn("", message, service.currentTime())
		if err != nil {
			return err
		}
	}
	finishError := func(turnErr error) error {
		return service.finishError(runtime, session, turn.ID, turnErr)
	}
	if targets := extractTargets(message); len(targets) != 0 {
		session.AddTargets(targets...)
	}
	if !resuming {
		if err := conversation.Append(core.Message{Role: core.RoleUser, Content: message}); err != nil {
			return finishError(err)
		}
	}
	if err := runtime.PersistState(session); err != nil {
		return finishError(err)
	}
	runtime.PublishSnapshot(session.ID)
	runtime.Emit(session.ID, projectturn.Event{TurnID: turn.ID, Kind: projectturn.EventTurnStarted, Message: message})

	projectTools, err := runtime.Tools(ctx)
	if err != nil {
		return finishError(err)
	}
	tools, err := newRuntimeToolProvider(runtime, session, projectTools).Tools(ctx)
	if err != nil {
		return finishError(err)
	}
	instruction := service.systemPrompt
	if service.skillContext != nil {
		instruction = strings.TrimSpace(instruction + "\n\n" + service.skillContext(message))
	}
	contextWindow := NewContextWindow(runtime.store, service.contextConfig, service.summarizer, estimateTextTokens(llm.SystemPrompt(instruction, ""))+estimateToolTokens(tools))
	chatModel, err := service.newModel(ctx)
	if err != nil {
		return finishError(err)
	}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "pentgo",
		Instruction:   llm.SystemPrompt(instruction, ""),
		Model:         chatModel,
		MaxIterations: service.maxRequests,
		Handlers: []adk.ChatModelAgentMiddleware{
			NewEvidenceMiddleware(runtime.Evidence()),
			NewContextMiddleware(ContextMiddlewareConfig{
				SessionID:    session.ID,
				Window:       contextWindow,
				Conversation: conversation.Messages,
				BuildTools:   func(context.Context) ([]core.Tool, error) { return tools, nil },
				Facts:        func(runCtx context.Context) (string, error) { return runtime.FactSnapshot(runCtx) },
			}),
		},
	})
	if err != nil {
		return finishError(err)
	}
	checkpointStore, err := storage.NewSQLiteCheckpointStore(runtime.store.DatabasePath(), session.ID, turn.ID)
	if err != nil {
		return finishError(err)
	}
	defer checkpointStore.Close()
	checkpointID := turn.ID
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true, CheckPointStore: checkpointStore})
	cancelOption, cancelRun := adk.WithCancel()
	service.registerCancel(session.ID, cancelRun)
	defer service.unregisterCancel(session.ID, cancelRun)
	bridge, err := NewEventBridge(conversation, session.ID, turn.ID, func(event projectturn.Event) {
		runtime.Emit(session.ID, event)
	})
	if err != nil {
		return finishError(err)
	}
	if resuming {
		iterator, resumeErr := runner.Resume(ctx, checkpointID, cancelOption)
		if resumeErr != nil {
			_ = checkpointStore.Delete(context.Background(), checkpointID)
			return finishError(resumeErr)
		}
		err = bridge.Consume(ctx, iterator)
	} else {
		err = bridge.Consume(ctx, runner.Run(ctx, toEinoMessages([]core.Message{{Role: core.RoleUser, Content: message}}), adk.WithCheckPointID(checkpointID), cancelOption))
	}
	if err != nil {
		var cancelErr *adk.CancelError
		if !errors.As(err, &cancelErr) {
			_ = checkpointStore.Delete(context.Background(), checkpointID)
		}
		return finishError(err)
	}
	messages := conversation.Messages()
	for _, persisted := range messages {
		if persisted.Role == core.RoleAssistant && len(persisted.ToolCalls) == 0 && strings.TrimSpace(persisted.Content) != "" {
			session.FinalSummary = strings.TrimSpace(persisted.Content)
		}
	}
	if session.FinalSummary == "" {
		return finishError(fmt.Errorf("runner turn ended without final assistant text"))
	}
	if session.ActiveTurn == nil || session.ActiveTurn.Status != sessionstate.TurnRunning {
		return fmt.Errorf("runner turn ended without a running session turn")
	}
	if err := session.FinishTurn(turn.ID, session.FinalSummary, service.currentTime()); err != nil {
		return err
	}
	if err := runtime.PersistState(session); err != nil {
		return err
	}
	if err := checkpointStore.Delete(context.Background(), checkpointID); err != nil {
		return fmt.Errorf("delete completed runner checkpoint: %w", err)
	}
	runtime.PublishSnapshot(session.ID)
	runtime.Emit(session.ID, projectturn.Event{TurnID: turn.ID, Kind: projectturn.EventTurnFinished, Message: session.FinalSummary})
	return nil
}

// PauseSession stops at the next safe point, escalating promptly when a model or tool is stuck.
func (service *TurnService) PauseSession(sessionID string) bool {
	if service == nil {
		return false
	}
	service.mu.Lock()
	cancel := service.cancellations[sessionID]
	service.mu.Unlock()
	if cancel == nil {
		return false
	}
	_, contributed := cancel(
		adk.WithAgentCancelMode(adk.CancelAfterChatModel|adk.CancelAfterToolCalls),
		adk.WithAgentCancelTimeout(time.Second),
		adk.WithRecursive(),
	)
	return contributed
}

func (service *TurnService) registerCancel(sessionID string, cancel adk.AgentCancelFunc) {
	service.mu.Lock()
	service.cancellations[sessionID] = cancel
	service.mu.Unlock()
}

func (service *TurnService) unregisterCancel(sessionID string, cancel adk.AgentCancelFunc) {
	service.mu.Lock()
	if service.cancellations[sessionID] != nil {
		delete(service.cancellations, sessionID)
	}
	service.mu.Unlock()
}

func (service *TurnService) finishError(runtime *ProjectRuntime, session *sessionstate.Session, turnID string, turnErr error) error {
	var cancelErr *adk.CancelError
	interrupted := errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) || errors.As(turnErr, &cancelErr)
	if session.ActiveTurn != nil && session.ActiveTurn.ID == turnID && session.ActiveTurn.Status == sessionstate.TurnRunning {
		if interrupted {
			_ = session.InterruptTurn(turnID, turnErr.Error(), service.currentTime())
		} else {
			_ = session.FailTurn(turnID, turnErr.Error(), service.currentTime())
		}
	}
	if persistErr := runtime.PersistState(session); persistErr != nil {
		return fmt.Errorf("%v; persist turn: %w", turnErr, persistErr)
	}
	runtime.PublishSnapshot(session.ID)
	if interrupted {
		runtime.Emit(session.ID, projectturn.Event{TurnID: turnID, Kind: projectturn.EventTurnFinished, Message: "turn interrupted"})
	} else {
		runtime.Emit(session.ID, projectturn.Event{TurnID: turnID, Kind: projectturn.EventTurnFailed, Message: turnErr.Error()})
	}
	return turnErr
}

func (service *TurnService) currentTime() time.Time { return service.now().UTC() }
