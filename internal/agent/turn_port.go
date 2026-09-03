package agent

import (
	"context"
	"fmt"

	contextpolicy "pentgo/internal/context"
)

func (runtime *ProjectRuntime) FactSnapshot(ctx context.Context) (string, error) {
	if runtime == nil || runtime.facts == nil {
		return "", fmt.Errorf("project fact index is unavailable")
	}
	facts, err := runtime.facts.List(ctx)
	if err != nil {
		return "", err
	}
	return contextpolicy.RenderProjectFactIndex(facts), nil
}
