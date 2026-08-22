package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

// EngineFactory 为一个会话 turn 创建新的模型引擎。运行时提供全部项目级依赖，
// 因此引擎本身保持无状态。
type EngineFactory func(context.Context, *domain.Session, *ProjectRuntime) (agent.ModelEngine, error)

// TurnService 编排一次可持久化的模型 turn：写入用户消息、执行模型、追加每条输出消息，
// 最后发布进度事件。
type TurnService struct {
	configMu        sync.RWMutex
	engine          agent.ModelEngine
	engineFactory   EngineFactory
	store           *storage.ProjectStore
	tools           agent.ToolProvider
	loadSkill       SkillLoader
	skillsAvailable bool
	now             func() time.Time
}

// NewTurnService 使用可选的固定测试依赖创建服务。生产环境通常由 EngineFactory
// 为每次 turn 构造新的引擎。
func NewTurnService(engine agent.ModelEngine, store *storage.ProjectStore, tools agent.ToolProvider) *TurnService {
	return &TurnService{engine: engine, store: store, tools: tools, now: func() time.Time { return time.Now().UTC() }}
}

// SetEngineFactory 配置按 turn 延迟构造模型引擎的工厂。
func (service *TurnService) SetEngineFactory(factory EngineFactory) {
	if service != nil {
		service.configMu.Lock()
		defer service.configMu.Unlock()
		service.engineFactory = factory
	}
}

// SetSkillCatalog configures the process-start discovered skill loader for later turns.
func (service *TurnService) SetSkillCatalog(load SkillLoader, available bool) {
	if service == nil {
		return
	}
	service.configMu.Lock()
	defer service.configMu.Unlock()
	service.loadSkill = load
	service.skillsAvailable = available
}

// SetClock 替换时钟，供编排逻辑进行确定性测试。
func (service *TurnService) SetClock(clock func() time.Time) {
	if service != nil && clock != nil {
		service.configMu.Lock()
		defer service.configMu.Unlock()
		service.now = clock
	}
}

// RunTurn 从持久化输入执行一次用户请求，直至得到最终模型响应。它会在发布 UI 事件前
// 持久化每条 transcript 消息，确保中断和回放看到完全一致的消息顺序。
func (service *TurnService) RunTurn(ctx context.Context, projectRuntime *ProjectRuntime, session *domain.Session, message string) error {
	if service == nil || projectRuntime == nil || session == nil {
		return fmt.Errorf("turn service dependencies are incomplete")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("user message is empty")
	}
	transcript := projectRuntime.Transcript(session.ID)
	if transcript == nil {
		return fmt.Errorf("session transcript is unavailable")
	}
	now := service.currentTime()
	turn, err := session.BeginTurn("", message, now)
	if err != nil {
		return err
	}
	if targets := extractTargets(message); len(targets) != 0 {
		session.AddTargets(targets...)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: message}); err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, now)
	}
	if err := service.persist(projectRuntime, session); err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, now)
	}
	projectRuntime.PublishSnapshot(session.ID)
	projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnStarted, Message: message})

	engine, err := service.resolveEngine(ctx, session, projectRuntime)
	if err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
	}
	externalTools, err := service.resolveTools(ctx, projectRuntime)
	if err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
	}
	loadSkill, skillsAvailable := service.skillConfig()
	toolProvider := newRuntimeToolProvider(projectRuntime, session, externalTools, loadSkill, skillsAvailable)
	tools, err := toolProvider.Tools(ctx)
	if err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
	}
	events, err := engine.Run(ctx, agent.TurnInput{SessionID: session.ID, Messages: transcript.Messages(), Tools: tools, ProjectFacts: blackboardText(projectRuntime.Blackboard())})
	if err != nil {
		return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
	}
	if events == nil {
		return service.finishError(projectRuntime, session, turn.ID, fmt.Errorf("model engine returned nil event stream"), service.currentTime())
	}
	finalSummary := ""
	for event := range events {
		if ctx.Err() != nil {
			return service.finishError(projectRuntime, session, turn.ID, ctx.Err(), service.currentTime())
		}
		if event.Err != nil || event.Kind == agent.TurnEventError {
			if event.Err == nil {
				event.Err = errors.New(event.Output)
			}
			return service.finishError(projectRuntime, session, turn.ID, event.Err, service.currentTime())
		}
		if event.Message.Role == "" {
			continue
		}
		if err := transcript.Append(event.Message); err != nil {
			return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
		}
		if event.Message.Role == agent.RoleAssistant {
			for _, call := range event.Message.ToolCalls {
				projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventToolStarted, Message: call.Name, Data: call})
			}
			if len(event.Message.ToolCalls) == 0 {
				finalSummary = strings.TrimSpace(event.Message.Content)
				projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventAssistantMessage, Message: finalSummary})
			}
		} else if event.Message.Role == agent.RoleTool {
			projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventToolFinished, Message: event.Message.ToolName, Output: event.Message.Content})
		}
		if err := service.persist(projectRuntime, session); err != nil {
			return service.finishError(projectRuntime, session, turn.ID, err, service.currentTime())
		}
		projectRuntime.PublishSnapshot(session.ID)
	}
	if strings.TrimSpace(finalSummary) == "" {
		if ctx.Err() != nil {
			return service.finishError(projectRuntime, session, turn.ID, ctx.Err(), service.currentTime())
		}
		return service.finishError(projectRuntime, session, turn.ID, fmt.Errorf("model engine ended without a final assistant message"), service.currentTime())
	}
	if err := session.FinishTurn(turn.ID, finalSummary, service.currentTime()); err != nil {
		return err
	}
	if err := service.persist(projectRuntime, session); err != nil {
		return err
	}
	projectRuntime.PublishSnapshot(session.ID)
	projectRuntime.Emit(session.ID, Event{TurnID: turn.ID, Kind: EventTurnFinished, Message: finalSummary})
	return nil
}

// resolveEngine 测试时优先使用固定引擎，否则创建不保留历史会话状态的新适配器。
func (service *TurnService) resolveEngine(ctx context.Context, session *domain.Session, projectRuntime *ProjectRuntime) (agent.ModelEngine, error) {
	service.configMu.RLock()
	engine, factory := service.engine, service.engineFactory
	service.configMu.RUnlock()
	if engine != nil {
		return engine, nil
	}
	if factory == nil {
		return nil, fmt.Errorf("model engine is not configured")
	}
	return factory(ctx, session, projectRuntime)
}

// resolveTools 允许测试注入工具；生产环境使用项目运行时持有的 MCP 工具。
func (service *TurnService) resolveTools(ctx context.Context, projectRuntime *ProjectRuntime) ([]agent.Tool, error) {
	if service.tools != nil {
		return service.tools.Tools(ctx)
	}
	return projectRuntime.Tools(ctx)
}

// persist 优先使用项目事务；未构造完整运行时的单元测试仍可走独立存储路径。
func (service *TurnService) persist(projectRuntime *ProjectRuntime, session *domain.Session) error {
	if projectRuntime.Store() == nil && service.store != nil {
		return service.store.SaveSession(session)
	}
	return projectRuntime.PersistState(session)
}

// finishError 在向 SessionWorker 返回原始执行错误前，先持久化中断或失败的 turn。
func (service *TurnService) finishError(projectRuntime *ProjectRuntime, session *domain.Session, turnID string, turnErr error, finishedAt time.Time) error {
	if session.ActiveTurn != nil && session.ActiveTurn.ID == turnID && session.ActiveTurn.Status == domain.TurnRunning {
		if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
			_ = session.InterruptTurn(turnID, turnErr.Error(), finishedAt)
		} else {
			_ = session.FailTurn(turnID, turnErr.Error(), finishedAt)
		}
	}
	if persistErr := service.persist(projectRuntime, session); persistErr != nil {
		return fmt.Errorf("%v; persist turn: %w", turnErr, persistErr)
	}
	projectRuntime.PublishSnapshot(session.ID)
	if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) {
		projectRuntime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFinished, Message: "turn interrupted"})
	} else {
		projectRuntime.Emit(session.ID, Event{TurnID: turnID, Kind: EventTurnFailed, Message: turnErr.Error()})
	}
	return turnErr
}

// currentTime 从配置时钟获取规范化的 UTC 时间。
func (service *TurnService) currentTime() time.Time {
	if service == nil {
		return time.Now().UTC()
	}
	service.configMu.RLock()
	clock := service.now
	service.configMu.RUnlock()
	if clock == nil {
		return time.Now().UTC()
	}
	return clock().UTC()
}

// skillConfig returns a consistent snapshot of startup-discovered skill availability.
func (service *TurnService) skillConfig() (SkillLoader, bool) {
	if service == nil {
		return nil, false
	}
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	return service.loadSkill, service.skillsAvailable
}
