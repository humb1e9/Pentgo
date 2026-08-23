package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	FactCategoryTarget   = "target"
	FactCategoryAuth     = "auth"
	FactCategoryInfra    = "infra"
	FactCategoryBusiness = "business"
	FactCategoryFinding  = "finding"
	FactCategoryChain    = "chain"
	FactCategoryExploit  = "exploit"
	FactCategoryPOC      = "poc"
	FactCategoryNote     = "note"

	FactConfidenceConfirmed  = "confirmed"
	FactConfidenceTentative  = "tentative"
	FactConfidenceDeprecated = "deprecated"

	FactEdgeDependsOn    = "depends_on"
	FactEdgeLeadsTo      = "leads_to"
	FactEdgeEnables      = "enables"
	FactEdgeExploits     = "exploits"
	FactEdgeDiscoveredOn = "discovered_on"
	FactEdgeContains     = "contains"
	FactEdgePartOf       = "part_of"
	FactEdgeSupports     = "supports"
)

// ProjectFact is a durable, project-scoped assertion. Summary is the bounded
// Fact Index projection; Body retains the complete reproducible detail.
type ProjectFact struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	FactKey         string    `json:"fact_key"`
	Category        string    `json:"category"`
	Summary         string    `json:"summary"`
	Body            string    `json:"body"`
	Confidence      string    `json:"confidence"`
	Pinned          bool      `json:"pinned"`
	SourceSessionID string    `json:"source_session_id,omitempty"`
	EvidenceRefs    []int     `json:"evidence_refs,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ProjectFactEdge relates two facts in one project. Edges remain auditable
// when deprecated, but the default Fact Index excludes them.
type ProjectFactEdge struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	SourceFactKey string    `json:"source_fact_key"`
	TargetFactKey string    `json:"target_fact_key"`
	EdgeType      string    `json:"edge_type"`
	Confidence    string    `json:"confidence"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ValidateProjectFact checks the durable fact contract before storage checks
// project ownership, Evidence success, and edge targets.
func ValidateProjectFact(fact ProjectFact) error {
	fact.FactKey = strings.TrimSpace(fact.FactKey)
	fact.Category = strings.TrimSpace(fact.Category)
	fact.Summary = strings.TrimSpace(fact.Summary)
	fact.Body = strings.TrimSpace(fact.Body)
	fact.Confidence = strings.TrimSpace(fact.Confidence)
	if fact.FactKey == "" || fact.Category == "" || fact.Summary == "" || fact.Body == "" || fact.Confidence == "" {
		return fmt.Errorf("fact key, category, summary, body, and confidence are required")
	}
	if !validFactCategory(fact.Category) {
		return fmt.Errorf("invalid fact category: %s", fact.Category)
	}
	if !validFactConfidence(fact.Confidence) {
		return fmt.Errorf("invalid fact confidence: %s", fact.Confidence)
	}
	seen := make(map[int]bool, len(fact.EvidenceRefs))
	for _, reference := range fact.EvidenceRefs {
		if reference <= 0 || seen[reference] {
			return fmt.Errorf("fact evidence refs must be positive and unique")
		}
		seen[reference] = true
	}
	if fact.Confidence == FactConfidenceConfirmed && len(fact.EvidenceRefs) == 0 {
		return fmt.Errorf("confirmed fact requires evidence refs")
	}
	return nil
}

func ValidateProjectFactEdge(edge ProjectFactEdge) error {
	edge.SourceFactKey = strings.TrimSpace(edge.SourceFactKey)
	edge.TargetFactKey = strings.TrimSpace(edge.TargetFactKey)
	edge.EdgeType = strings.TrimSpace(edge.EdgeType)
	edge.Confidence = strings.TrimSpace(edge.Confidence)
	if edge.SourceFactKey == "" || edge.TargetFactKey == "" || edge.EdgeType == "" || edge.Confidence == "" {
		return fmt.Errorf("edge source, target, type, and confidence are required")
	}
	if !validFactEdgeType(edge.EdgeType) {
		return fmt.Errorf("invalid fact edge type: %s", edge.EdgeType)
	}
	if !validFactConfidence(edge.Confidence) {
		return fmt.Errorf("invalid fact edge confidence: %s", edge.Confidence)
	}
	return nil
}

func CloneProjectFact(source ProjectFact) ProjectFact {
	result := source
	result.EvidenceRefs = append([]int(nil), source.EvidenceRefs...)
	return result
}

func CloneProjectFactEdge(source ProjectFactEdge) ProjectFactEdge { return source }

func NormalizeEvidenceRefs(references []int) []int {
	result := append([]int(nil), references...)
	sort.Ints(result)
	return result
}

func validFactCategory(category string) bool {
	switch category {
	case FactCategoryTarget, FactCategoryAuth, FactCategoryInfra, FactCategoryBusiness, FactCategoryFinding, FactCategoryChain, FactCategoryExploit, FactCategoryPOC, FactCategoryNote:
		return true
	default:
		return false
	}
}

func validFactConfidence(confidence string) bool {
	switch confidence {
	case FactConfidenceConfirmed, FactConfidenceTentative, FactConfidenceDeprecated:
		return true
	default:
		return false
	}
}

func validFactEdgeType(kind string) bool {
	switch kind {
	case FactEdgeDependsOn, FactEdgeLeadsTo, FactEdgeEnables, FactEdgeExploits, FactEdgeDiscoveredOn, FactEdgeContains, FactEdgePartOf, FactEdgeSupports:
		return true
	default:
		return false
	}
}
