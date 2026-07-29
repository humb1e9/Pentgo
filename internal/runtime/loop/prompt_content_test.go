package loop

import (
	"strings"
	"testing"
)

func TestSystemPromptDefinesNaturalToolContract(t *testing.T) {
	prompt := basePromptContent()
	for _, want := range []string{"exec", "execute_python", "load_skill", "record_finding", "evidence_ref"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, banned := range []string{"execute" + "_code", "complete" + "_task", "evidence" + "_gate", "declare" + "_session", "TASK" + "_COMPLETE", "MISSION" + "_COMPLETE"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("contains %q", banned)
		}
	}
}
