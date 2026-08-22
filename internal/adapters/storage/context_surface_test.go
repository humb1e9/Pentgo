package storage

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

func TestContextSurfaceStartsAsRawTranscriptCoverage(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()

	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	snapshot, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceRanges(snapshot.Nodes); !reflect.DeepEqual(got, [][2]int{{1, 1}, {2, 2}, {3, 3}, {4, 4}}) {
		t.Fatalf("source ranges = %v", got)
	}
	for _, node := range snapshot.Nodes {
		if node.Kind != agent.SurfaceNodeSource || node.Generation != 0 {
			t.Fatalf("node = %#v", node)
		}
	}
}

func TestContextSurfaceReplacePersistsAndRawTranscriptRemainsUntouched(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()

	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surface.StartCompaction(0, 1, 3); err != nil {
		t.Fatal(err)
	}
	snapshot, err := surface.ReplaceRange(0, 1, 3, agent.SurfaceNode{
		ID:             "checkpoint-1",
		Kind:           agent.SurfaceNodeCheckpoint,
		SourceStartSeq: 1,
		SourceEndSeq:   3,
		Content:        "checkpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := surface.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot, err = reopened.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 2 || snapshot.Nodes[0].Kind != agent.SurfaceNodeCheckpoint || snapshot.Nodes[0].Content != "checkpoint" || snapshot.Nodes[1].SourceStartSeq != 4 {
		t.Fatalf("surface = %#v", snapshot)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer transcript.Close()
	messages := transcript.Messages()
	if len(messages) != 4 || messages[0].Content != "user" || messages[1].ToolCalls[0].Name != "probe" || messages[2].Content != "tool result" || messages[3].Content != "assistant" {
		t.Fatalf("raw transcript = %#v", messages)
	}
}

func TestContextSurfaceAppendsNewRawTranscriptCoverage(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: "later"}); err != nil {
		_ = transcript.Close()
		t.Fatal(err)
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceRanges(snapshot.Nodes); !reflect.DeepEqual(got, [][2]int{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}}) {
		t.Fatalf("source ranges = %v", got)
	}
}

func TestContextSurfacePruneToolsRollsBackAllNodesWhenAnySourceIsInvalid(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	before, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surface.PruneTools(before.Generation, map[int]string{3: "short tool result", 1: "invalid user result"}); !errors.Is(err, ErrInvalidSurfaceRange) {
		t.Fatalf("prune error = %v", err)
	}
	after, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("surface changed after batch prune rejection: %#v", after)
	}
}

func TestContextSurfaceRejectsRangeThatSplitsToolPair(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	for _, selected := range [][2]int{{1, 2}, {2, 2}, {3, 3}, {3, 4}} {
		if _, err := surface.StartCompaction(0, selected[0], selected[1]); err == nil {
			t.Fatalf("split tool pair range %v was accepted", selected)
		}
	}
	if _, err := surface.StartCompaction(0, 2, 3); err != nil {
		t.Fatalf("balanced tool pair rejected: %v", err)
	}
}

func TestContextSurfaceReplaceRequiresStartedLifecycle(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	before, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(before.Generation, 1, 2, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 2, Content: "checkpoint"}); err == nil {
		t.Fatal("replacement without started lifecycle succeeded")
	}
	after, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("surface changed without lifecycle: %#v", after)
	}
}

func TestContextSurfaceAppendedSourceUsesCurrentGeneration(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	if _, err := surface.StartCompaction(0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 1, 3, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 3, Content: "checkpoint"}); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: "later"}); err != nil {
		_ = transcript.Close()
		t.Fatal(err)
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	last := snapshot.Nodes[len(snapshot.Nodes)-1]
	if last.Kind != agent.SurfaceNodeSource || last.SourceStartSeq != 5 || last.Generation != snapshot.Generation {
		t.Fatalf("appended node = %#v; surface generation = %d", last, snapshot.Generation)
	}
}

func TestContextSurfaceRejectsStaleGeneration(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	if _, err := surface.StartCompaction(0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 1, 3, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 3, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 3, 4, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 3, SourceEndSeq: 4, Content: "stale"}); err == nil {
		t.Fatal("stale generation replacement succeeded")
	}
}

func TestContextSurfaceStaleMutationDoesNotSynchronizeNewSourceNodes(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer surface.Close()
	if _, err := surface.StartCompaction(0, 1, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.ReplaceRange(0, 1, 3, agent.SurfaceNode{Kind: agent.SurfaceNodeCheckpoint, SourceStartSeq: 1, SourceEndSeq: 3, Content: "checkpoint"}); err != nil {
		t.Fatal(err)
	}
	before, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcript.Append(agent.Message{Role: agent.RoleUser, Content: "new raw source"}); err != nil {
		_ = transcript.Close()
		t.Fatal(err)
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := surface.PruneTool(0, 5, "pruned"); !errors.Is(err, ErrStaleSurfaceGeneration) {
		t.Fatalf("stale prune error = %v", err)
	}
	if got, err := loadContextSurface(surface.db, session.ID); err != nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("surface after stale prune = %#v/%v, want %#v", got, err, before)
	}
}

func TestContextSurfaceRestoresUnfinishedCompactionWithoutMutation(t *testing.T) {
	store, session := surfaceTranscript(t)
	defer store.Close()
	surface, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := surface.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surface.StartCompaction(before.Generation, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := surface.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenContextSurface(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("recovered surface = %#v, want %#v", after, before)
	}
	if got, err := reopened.Compactions(); err != nil || len(got) != 1 || got[0].Status != agent.CompactionFailed {
		t.Fatalf("compactions/error = %#v/%v", got, err)
	}
}

func surfaceTranscript(t *testing.T) (*ProjectStore, *domain.Session) {
	t.Helper()
	store := newTestStore(t)
	session := domain.NewSession("surface-session", "test", time.Now().UTC())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.Message{
		{Role: agent.RoleUser, Content: "user"},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "probe", Arguments: map[string]any{"target": "TARGET"}}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "probe", Content: "tool result"},
		{Role: agent.RoleAssistant, Content: "assistant"},
	} {
		if err := transcript.Append(message); err != nil {
			_ = transcript.Close()
			t.Fatal(err)
		}
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	return store, session
}

func sourceRanges(nodes []agent.SurfaceNode) [][2]int {
	ranges := make([][2]int, 0, len(nodes))
	for _, node := range nodes {
		ranges = append(ranges, [2]int{node.SourceStartSeq, node.SourceEndSeq})
	}
	return ranges
}
