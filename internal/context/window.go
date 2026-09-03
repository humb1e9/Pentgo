package context

import (
	stdcontext "context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"pentgo/internal/project"
	"pentgo/internal/session"
)

// SummaryInput is the text-only request used to update a rolling context summary.
type SummaryInput struct {
	PriorSummary string
	Messages     []session.Message
	MaxTokens    int
}

// SummaryWriter compresses an older conversation prefix without exposing tools.
type SummaryWriter interface {
	Summarize(stdcontext.Context, SummaryInput) (string, error)
}

// SummaryStore persists the rolling summary for one session.
type SummaryStore interface {
	LoadContextSummary(stdcontext.Context, string) (project.ContextSummary, bool, error)
	SaveContextSummary(stdcontext.Context, project.ContextSummary) error
}

// ContextWindow assembles a durable summary with a recent raw-message window.
type ContextWindow struct {
	store          SummaryStore
	contextWindow  int
	fixedTokens    int
	recentMessages int
	summaryTokens  int
	summarizer     SummaryWriter
	now            func() time.Time
}

func NewContextWindow(store SummaryStore, cfg project.ContextConfig, summarizer SummaryWriter, fixedTokens int) *ContextWindow {
	cfg = cfg.Effective()
	return &ContextWindow{store: store, contextWindow: cfg.ContextWindow, fixedTokens: fixedTokens, recentMessages: cfg.RecentMessages, summaryTokens: cfg.SummaryMaxTokens, summarizer: summarizer, now: time.Now}
}

func (window *ContextWindow) Messages(ctx stdcontext.Context, sessionID string, conversation []session.Message, facts string) ([]session.Message, error) {
	if window == nil || window.store == nil {
		return nil, fmt.Errorf("context window dependencies are incomplete")
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("context window session ID is empty")
	}
	summary, found, err := window.store.LoadContextSummary(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if window.contextWindow <= 0 {
		return nil, fmt.Errorf("context_window must be > 0")
	}
	fixedTokens := window.fixedTokens
	if strings.TrimSpace(facts) != "" {
		fixedTokens += EstimateMessageTokens([]session.Message{{Role: session.RoleSystem, Content: "Project facts:\n" + strings.TrimSpace(facts)}})
	}
	summaryReserve := EstimateMessageTokens([]session.Message{{Role: session.RoleSystem, Content: "Conversation summary:\n"}}) + window.summaryTokens
	if fixedTokens+summaryReserve >= window.contextWindow {
		return nil, fmt.Errorf("fixed model context exceeds configured context_window")
	}
	keepFrom := len(conversation) - window.recentMessages
	if keepFrom < 0 {
		keepFrom = 0
	}
	if found && summary.CoveredThroughSeq > keepFrom {
		keepFrom = summary.CoveredThroughSeq
	}
	keepFrom = toolSafeBoundary(conversation, keepFrom)
	for EstimateMessageTokens(conversation[keepFrom:]) > window.contextWindow-fixedTokens-summaryReserve {
		next := nextToolSafeBoundary(conversation, keepFrom)
		if next <= keepFrom {
			return nil, fmt.Errorf("recent model context exceeds configured context_window")
		}
		keepFrom = next
	}
	if found {
		summary.Content = truncateSummaryTokens(summary.Content, window.summaryTokens)
	}
	if keepFrom > len(conversation) {
		keepFrom = len(conversation)
	}
	if keepFrom > summary.CoveredThroughSeq {
		input := SummaryInput{PriorSummary: summary.Content, Messages: cloneMessages(conversation[summary.CoveredThroughSeq:keepFrom]), MaxTokens: window.summaryTokens}
		content := ""
		if window.summarizer != nil {
			content, _ = window.summarizer.Summarize(ctx, input)
		}
		if strings.TrimSpace(content) == "" {
			content = fallbackSummary(input)
		}
		content = truncateSummaryTokens(content, window.summaryTokens)
		summary = project.ContextSummary{SessionID: sessionID, CoveredThroughSeq: keepFrom, Content: content, UpdatedAt: window.now().UTC()}
		if err := window.store.SaveContextSummary(ctx, summary); err != nil {
			return nil, err
		}
		found = true
	}
	messages := make([]session.Message, 0, len(conversation)-keepFrom+2)
	if strings.TrimSpace(facts) != "" {
		messages = append(messages, session.Message{Role: session.RoleSystem, Content: "Project facts:\n" + strings.TrimSpace(facts)})
	}
	if found && strings.TrimSpace(summary.Content) != "" {
		messages = append(messages, session.Message{Role: session.RoleSystem, Content: "Conversation summary:\n" + strings.TrimSpace(summary.Content)})
	}
	messages = append(messages, cloneMessages(conversation[keepFrom:])...)
	if window.fixedTokens+EstimateMessageTokens(messages) > window.contextWindow {
		return nil, fmt.Errorf("assembled model context exceeds configured context_window")
	}
	return messages, nil
}

// fallbackSummary keeps turn progress independent from an unavailable or invalid summary model.
func fallbackSummary(input SummaryInput) string {
	limit := input.MaxTokens * 4
	if limit <= 0 {
		limit = 4096
	}
	var builder strings.Builder
	if prior := strings.TrimSpace(input.PriorSummary); prior != "" {
		builder.WriteString(prior)
	}
	for _, message := range input.Messages {
		if builder.Len() != 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "[%s] %s", message.Role, strings.TrimSpace(message.Content))
		if utf8.RuneCountInString(builder.String()) >= limit {
			break
		}
	}
	return truncateSummaryTokens(builder.String(), input.MaxTokens)
}

func truncateSummaryTokens(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || EstimateTextTokens(value) <= limit {
		return value
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := (low + high + 1) / 2
		if EstimateTextTokens(string(runes[:middle])) <= limit {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return string(runes[:low])
}

func toolSafeBoundary(messages []session.Message, index int) int {
	for index > 0 && index < len(messages) && messages[index].Role == session.RoleTool {
		index--
	}
	return index
}

func nextToolSafeBoundary(messages []session.Message, index int) int {
	if index >= len(messages) {
		return index
	}
	index++
	for index < len(messages) && messages[index].Role == session.RoleTool {
		index++
	}
	return index
}

func cloneMessages(messages []session.Message) []session.Message {
	cloned := make([]session.Message, 0, len(messages))
	for _, message := range messages {
		cloned = append(cloned, session.CloneMessage(message))
	}
	return cloned
}
