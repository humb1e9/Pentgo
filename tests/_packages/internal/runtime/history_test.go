package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHistoryKeepsMissionContextAndBoundsFollowingMessages(t *testing.T) {
	history := NewHistory("https://example.com", "检查公开入口")
	for index := 0; index < 20; index++ {
		history.Append("assistant", strings.Repeat("x", 3500))
	}

	messages := history.Messages()
	if len(messages) != 17 {
		t.Fatalf("message count = %d, want 17", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "TARGET: https://example.com\nTASK: 检查公开入口" {
		t.Fatalf("mission context = %+v", messages[0])
	}
	for _, message := range messages[1:] {
		if len(message.Content) != 3000 {
			t.Fatalf("message content len = %d", len(message.Content))
		}
	}
}

func TestHistoryTruncatesAtUTF8Boundary(t *testing.T) {
	history := NewHistory("https://example.com", "检查")
	history.Append("assistant", "a"+strings.Repeat("测", 1000))
	messages := history.Messages()
	if !utf8.ValidString(messages[1].Content) || len(messages[1].Content) > 3000 {
		t.Fatalf("message = %q", messages[1].Content)
	}
}
