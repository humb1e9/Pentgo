package project

import (
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

const (
	MaxProjectFactKeyRunes   = 64
	MaxProjectFactValueRunes = 16 * 1024
)

var projectFactKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ProjectFact is a durable, project-scoped key/value assertion.
type ProjectFact struct {
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	EvidenceRef *int      `json:"evidence_ref,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ValidateProjectFactKey checks the stable project-fact key namespace.
func ValidateProjectFactKey(key string) error {
	if key == "" {
		return fmt.Errorf("fact key is required")
	}
	if !projectFactKeyPattern.MatchString(key) {
		return fmt.Errorf("fact key must match ^[a-z][a-z0-9_]{0,63}$")
	}
	if utf8.RuneCountInString(key) > MaxProjectFactKeyRunes {
		return fmt.Errorf("fact key exceeds %d runes", MaxProjectFactKeyRunes)
	}
	return nil
}

// ValidateProjectFact checks the minimal key/value/evidence contract before
// persistence.
func ValidateProjectFact(fact ProjectFact) error {
	if err := ValidateProjectFactKey(fact.Key); err != nil {
		return err
	}
	if fact.Value == "" {
		return fmt.Errorf("fact value must be non-empty")
	}
	if utf8.RuneCountInString(fact.Value) > MaxProjectFactValueRunes {
		return fmt.Errorf("fact value exceeds %d runes", MaxProjectFactValueRunes)
	}
	if fact.EvidenceRef != nil && *fact.EvidenceRef <= 0 {
		return fmt.Errorf("fact evidence ref must be nil or positive")
	}
	return nil
}
