package runtime

import (
	"reflect"
	"testing"
)

func TestExtractFindingLabels(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []EvidenceLevel
	}{
		{"single likely", "[LIKELY] found an open API", []EvidenceLevel{EvidenceLikely}},
		{"mixed order dedup", "[VERIFIED] a\n[INFERRED] b\n[VERIFIED] c", []EvidenceLevel{EvidenceVerified, EvidenceInferred}},
		{"none", "just reconnaissance, no findings", nil},
		{"all three", "[VERIFIED] x [LIKELY] y [INFERRED] z", []EvidenceLevel{EvidenceVerified, EvidenceLikely, EvidenceInferred}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractFindingLabels(test.text); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("extractFindingLabels(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}
