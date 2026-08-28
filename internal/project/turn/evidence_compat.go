package turn

import "pentgo/internal/evidence"

// Deprecated: use internal/evidence directly.
type EvidenceStore = evidence.EvidenceStore

// Deprecated: use internal/evidence directly.
type Record = evidence.Record

// Deprecated: use evidence.ErrWrite.
var ErrWrite = evidence.ErrWrite

// Deprecated: use evidence.OpenEvidenceStore.
func OpenEvidenceStore(path string, secrets ...string) (*EvidenceStore, error) {
	return evidence.OpenEvidenceStore(path, secrets...)
}
