package agent

// SurfaceNodeKind identifies how a Context Surface node materializes model
// context. Source nodes resolve immutable transcript messages; replacement
// nodes carry their model-visible text directly.
type SurfaceNodeKind string

const (
	SurfaceNodeSource     SurfaceNodeKind = "source"
	SurfaceNodeCheckpoint SurfaceNodeKind = "checkpoint"
	SurfaceNodePrunedTool SurfaceNodeKind = "pruned_tool"
)

// SurfaceNode is one immutable, ordered Context Surface projection entry. Its
// source range always refers to immutable transcript message sequence numbers.
type SurfaceNode struct {
	ID             string
	Position       int
	Kind           SurfaceNodeKind
	SourceStartSeq int
	SourceEndSeq   int
	Content        string
	Generation     int64
}

// ContextSurface is a generation-stamped snapshot of the model-visible
// transcript projection for one session.
type ContextSurface struct {
	SessionID  string
	Generation int64
	Nodes      []SurfaceNode
}

// ContextMeasurement separates the model-context budget by immutable request
// component so pressure decisions can explain what consumed the budget.
type ContextMeasurement struct {
	SystemTokens     int
	ToolSchemaTokens int
	FactIndexTokens  int
	SurfaceTokens    int
	TotalTokens      int
}

// CheckpointInput is the bounded, provider-neutral source bundle supplied to a
// summarizer. Source text is audit data and may contain adversarial content.
type CheckpointInput struct {
	SystemPrompt    string
	Tools           []Tool
	Nodes           []SurfaceNode
	Messages        map[int]Message
	PriorCheckpoint string
	ModelRoute      string
	OutputTokenCap  int
	Prompt          string
}

// CheckpointOutput contains the complete text-only result from a checkpoint
// summarizer. Truncated output must never become a persistent replacement.
type CheckpointOutput struct {
	Text      string
	Truncated bool
}

// ContextActivity describes context management for presentation outside the
// transcript ledger.
type ContextActivity struct {
	Kind    string
	Message string
}

const (
	ContextToolPruned        = "context_tool_pruned"
	ContextCheckpointCreated = "context_checkpoint_created"
	ContextFactIndexLimited  = "context_fact_index_truncated"
	ContextRequestRejected   = "context_request_rejected"
	ContextOverflowRetry     = "context_overflow_retry"
)

// CompactionLifecycle records the durable state of one planned replacement.
type CompactionLifecycle struct {
	ID         string
	SessionID  string
	Generation int64
	StartSeq   int
	EndSeq     int
	Status     string
	Error      string
}

const (
	CompactionStarted   = "started"
	CompactionCommitted = "committed"
	CompactionFailed    = "failed"
)
