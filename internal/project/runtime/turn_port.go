package runtime

import (
	"context"
	"fmt"

	sessionstate "pentgo/internal/project/session"
	projectturn "pentgo/internal/project/turn"
)

func (runtime *ProjectRuntime) FactSnapshot(ctx context.Context) (string, error) {
	if runtime == nil || runtime.ProjectFactIndex() == nil {
		return "", fmt.Errorf("project fact index is unavailable")
	}
	return runtime.ProjectFactIndex().Snapshot(ctx)
}
func (runtime *ProjectRuntime) Persist(session *sessionstate.Session) error {
	return runtime.PersistState(session)
}
func (runtime *ProjectRuntime) Publish(sessionID string) { runtime.PublishSnapshot(sessionID) }

var _ projectturn.Runtime = (*ProjectRuntime)(nil)
