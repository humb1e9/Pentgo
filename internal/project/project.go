package project

import "time"

// SessionSummary is a project-level, reconstructable session index entry.
type SessionSummary struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Project aggregates sessions and records project-level identity and timing.
type Project struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Sessions  []SessionSummary `json:"sessions,omitempty"`
}

// CloneProject copies the reconstructable session summary index.
func CloneProject(source *Project) *Project {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Sessions = append([]SessionSummary(nil), source.Sessions...)
	return &cloned
}
