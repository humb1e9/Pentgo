package model

import (
	"context"
	"errors"

	"pentgo/internal/session"
	"pentgo/internal/tools"
)

type StepInput struct {
	SessionID       string
	Messages        []session.Message
	SystemPrompt    string
	ProjectFacts    string
	Tools           []tools.Tool
	MaxOutputTokens int
}

type StreamEvent struct {
	Delta        session.Message
	Final        *session.Message
	FinishReason string
	Err          error
}

var ErrContextWindowExceeded = errors.New("model context window exceeded")

type Stepper interface {
	StreamStep(context.Context, StepInput) (<-chan StreamEvent, error)
}
