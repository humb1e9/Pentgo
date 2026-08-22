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
	BlackboardTokens int
	SurfaceTokens    int
	TotalTokens      int
}

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
