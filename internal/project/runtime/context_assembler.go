package runtime

import (
	"fmt"

	"pentgo/internal/core"
	"pentgo/internal/project"
	projectcontext "pentgo/internal/project/context"
)

type ContextPreparer = projectcontext.ContextPreparer
type ContextAssembler = projectcontext.ContextAssembler
type ContextRequest = projectcontext.Request
type ContextMeter = projectcontext.Meter
type CheckpointSummarizer = projectcontext.CheckpointSummarizer
type ContextCompactor = projectcontext.ContextCompactor

var ErrContextPreflight = projectcontext.ErrContextPreflight

type runtimeContextSource struct{ runtime *ProjectRuntime }

func (source runtimeContextSource) Conversation(sessionID string) ([]core.Message, error) {
	conversation := source.runtime.Conversation(sessionID)
	if conversation == nil {
		return nil, fmt.Errorf("session conversation is unavailable")
	}
	return conversation.Messages(), nil
}

func (source runtimeContextSource) ContextSurface(sessionID string) (*projectcontext.ContextSurfaceStore, error) {
	surface := source.runtime.ContextSurface(sessionID)
	if surface == nil {
		return nil, fmt.Errorf("session context surface is unavailable")
	}
	return surface, nil
}

func NewContextAssembler(runtime *ProjectRuntime, policy project.ContextConfig, meter ContextMeter, summarizer CheckpointSummarizer) *ContextAssembler {
	return projectcontext.NewContextAssembler(runtimeContextSource{runtime}, projectContextPolicy(policy), meter, summarizer)
}

func NewContextCompactor(policy project.ContextConfig, surface *projectcontext.ContextSurfaceStore, meter ContextMeter, summarizer CheckpointSummarizer) *ContextCompactor {
	return projectcontext.NewContextCompactor(projectContextPolicy(policy), surface, meter, summarizer)
}

func NewContextMeter() ContextMeter { return projectcontext.NewMeter() }

func contextRequestFromInput(input core.ModelStepInput) ContextRequest {
	return projectcontext.RequestFromInput(input)
}

func contextThreshold(policy project.ContextConfig) int {
	return projectcontext.ContextThreshold(projectContextPolicy(policy))
}
