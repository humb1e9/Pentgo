package turn

import (
	"context"
	"fmt"
	"sort"

	projectmodel "pentgo/internal/project"
)

// ProjectFactUpsert is the typed command for upserting a project fact.
type ProjectFactUpsert struct {
	Key         string
	Value       string
	EvidenceRef *int
}

// ProjectFactRepository is the storage boundary for project facts.
type ProjectFactRepository interface {
	Upsert(context.Context, projectmodel.ProjectFact) error
	Get(context.Context, string) (projectmodel.ProjectFact, bool, error)
	List(context.Context) ([]projectmodel.ProjectFact, error)
}

// EvidenceReferenceLookup checks whether an evidence sequence exists.
type EvidenceReferenceLookup interface {
	Exists(seq int) bool
}

// ProjectFactLedger enforces business rules on project facts before passing
// them to the repository. It does not own the repository or evidence lookup.
type ProjectFactLedger struct {
	repo     ProjectFactRepository
	evidence EvidenceReferenceLookup
}

// NewProjectFactLedger creates a ledger with its required dependencies.
func NewProjectFactLedger(repo ProjectFactRepository, evidence EvidenceReferenceLookup) *ProjectFactLedger {
	return &ProjectFactLedger{repo: repo, evidence: evidence}
}

// Upsert validates the command, checks evidence existence, and delegates to
// the repository. A nil evidence ref is accepted; missing refs are rejected.
func (ledger *ProjectFactLedger) Upsert(ctx context.Context, cmd ProjectFactUpsert) error {
	if ledger == nil || ledger.repo == nil {
		return fmt.Errorf("project fact ledger is incomplete")
	}
	var evidenceRef *int
	if cmd.EvidenceRef != nil {
		value := *cmd.EvidenceRef
		evidenceRef = &value
	}
	fact := projectmodel.ProjectFact{
		Key:         cmd.Key,
		Value:       cmd.Value,
		EvidenceRef: evidenceRef,
	}
	if err := projectmodel.ValidateProjectFact(fact); err != nil {
		return err
	}
	if evidenceRef != nil {
		if ledger.evidence == nil {
			return fmt.Errorf("project fact evidence lookup is unavailable")
		}
		if !ledger.evidence.Exists(*evidenceRef) {
			return fmt.Errorf("evidence ref %d does not exist", *evidenceRef)
		}
	}
	return ledger.repo.Upsert(ctx, fact)
}

// Get returns the fact for the given key, or false if it does not exist.
func (ledger *ProjectFactLedger) Get(ctx context.Context, key string) (projectmodel.ProjectFact, bool, error) {
	if ledger == nil || ledger.repo == nil {
		return projectmodel.ProjectFact{}, false, fmt.Errorf("project fact ledger is incomplete")
	}
	if err := projectmodel.ValidateProjectFactKey(key); err != nil {
		return projectmodel.ProjectFact{}, false, err
	}
	return ledger.repo.Get(ctx, key)
}

// List returns all facts sorted by key ascending.
func (ledger *ProjectFactLedger) List(ctx context.Context) ([]projectmodel.ProjectFact, error) {
	if ledger == nil || ledger.repo == nil {
		return nil, fmt.Errorf("project fact ledger is incomplete")
	}
	facts, err := ledger.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(facts, func(left, right int) bool { return facts[left].Key < facts[right].Key })
	return facts, nil
}
