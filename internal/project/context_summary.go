package project

import "time"

// ContextSummary is the durable compressed prefix of one session conversation.
type ContextSummary struct {
	SessionID         string
	CoveredThroughSeq int
	Content           string
	UpdatedAt         time.Time
}
