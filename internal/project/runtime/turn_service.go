package runtime

import (
	"context"
	"time"

	"pentgo/internal/core"
	projectcontext "pentgo/internal/project/context"
	sessionstate "pentgo/internal/project/session"
	projectturn "pentgo/internal/project/turn"
)

type StepperFactory func(context.Context, *sessionstate.Session, *ProjectRuntime) (core.ModelStepper, error)
type TurnServiceConfig struct {
	StepperFactory  StepperFactory
	LoadSkill       SkillLoader
	SkillsAvailable bool
	Clock           func() time.Time
	MaxRequests     int
	SystemPrompt    string
	Assembler       ContextPreparer
}
type TurnService = projectturn.TurnService

func NewTurnService(stepper core.ModelStepper, _ any, tools core.ToolProvider, cfg TurnServiceConfig) *TurnService {
	return projectturn.NewTurnService(stepper, tools, projectturn.TurnServiceConfig{
		StepperFactory: func(ctx context.Context, session *sessionstate.Session, runtime projectturn.Runtime) (core.ModelStepper, error) {
			return cfg.StepperFactory(ctx, session, runtime.(*ProjectRuntime))
		},
		BuildTools: func(ctx context.Context, runtime projectturn.Runtime, session *sessionstate.Session, external []core.Tool) ([]core.Tool, error) {
			return newRuntimeToolProvider(runtime.(*ProjectRuntime), session, external, cfg.LoadSkill, cfg.SkillsAvailable).Tools(ctx)
		},
		Clock: cfg.Clock, MaxRequests: cfg.MaxRequests, SystemPrompt: cfg.SystemPrompt,
		Assembler: projectcontext.ContextPreparer(cfg.Assembler),
	})
}

func consumeModelStream(ctx context.Context, runtime *ProjectRuntime, sessionID, turnID string, stream <-chan core.ModelStreamEvent) (*core.Message, error) {
	return projectturn.ConsumeModelStream(ctx, runtime, sessionID, turnID, stream)
}

type toolResult struct {
	message core.Message
	err     error
}

func invokeToolCalls(ctx context.Context, runtime *ProjectRuntime, tools []core.Tool, calls []core.ToolCall) []toolResult {
	results := projectturn.InvokeToolCalls(ctx, runtime, tools, calls)
	mapped := make([]toolResult, len(results))
	for index, result := range results {
		mapped[index] = toolResult{message: result.Message, err: result.Err}
	}
	return mapped
}
