package loop

import (
	"reflect"
	"testing"

	"pentgo/internal/runtime/exec"
)


func TestExtractFindingLabels(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []exec.EvidenceLevel
	}{
		{"single likely", "[LIKELY] found an open API", []exec.EvidenceLevel{exec.EvidenceLikely}},
		{"mixed order dedup", "[VERIFIED] a\n[INFERRED] b\n[VERIFIED] c", []exec.EvidenceLevel{exec.EvidenceVerified, exec.EvidenceInferred}},
		{"none", "just reconnaissance, no findings", nil},
		{"all three", "[VERIFIED] x [LIKELY] y [INFERRED] z", []exec.EvidenceLevel{exec.EvidenceVerified, exec.EvidenceLikely, exec.EvidenceInferred}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractFindingLabels(test.text); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("extractFindingLabels(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}
