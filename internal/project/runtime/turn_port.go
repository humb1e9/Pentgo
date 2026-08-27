package runtime

import (
	"context"
	"fmt"
)

func (runtime *ProjectRuntime) FactSnapshot(ctx context.Context) (string, error) {
	if runtime == nil || runtime.ProjectFactIndex() == nil {
		return "", fmt.Errorf("project fact index is unavailable")
	}
	return runtime.ProjectFactIndex().Snapshot(ctx)
}
