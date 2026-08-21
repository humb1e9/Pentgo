package storage

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

func TestTranscriptStoreRoundTripsNormalizedMessages(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-transcript", "inspect", testTime())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.Message{
		{Role: agent.RoleUser, Content: "inspect TARGET"},
		{
			Role: agent.RoleAssistant,
			ToolCalls: []agent.ToolCall{{
				ID: "call-1", Name: "fixture_tool",
				Arguments: map[string]any{"target": "TARGET", "ports": []any{80.0, 443.0}},
			}},
		},
		{
			Role: agent.RoleTool, Content: "finished", ToolCallID: "call-1",
			ToolName: "fixture_tool", ToolArguments: map[string]any{"target": "TARGET"},
		},
	}
	for _, message := range want {
		if err := transcript.Append(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Messages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestTranscriptStoresAllocateSequencesAcrossHandles(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-concurrent-transcript", "inspect", testTime())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	first, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// 独立连接模拟并发运行的会话和运行时路径；SQLite 必须为每条消息分配不重复的序号。
	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for index, transcript := range []*TranscriptStore{first, second} {
		index, transcript := index, transcript
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- transcript.Append(agent.Message{Role: agent.RoleUser, Content: fmt.Sprintf("message-%d", index)})
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := loadTranscriptDB(store.db, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("message count = %d, want 2", len(loaded))
	}
}

func TestTranscriptMessagesReturnsDeepCopy(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	session := domain.NewSession("session-copy", "inspect", testTime())
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	transcript, err := store.OpenTranscript(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer transcript.Close()
	if err := transcript.Append(agent.Message{Role: agent.RoleTool, ToolArguments: map[string]any{"nested": map[string]any{"value": "original"}}}); err != nil {
		t.Fatal(err)
	}
	first := transcript.Messages()
	first[0].ToolArguments["nested"].(map[string]any)["value"] = "changed"
	second := transcript.Messages()
	if got := second[0].ToolArguments["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("stored value = %v", got)
	}
}

// testTime 保证各测试中的序列化时间戳断言具有确定性。
func testTime() time.Time {
	return time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
}
