package exec

import (
	"encoding/json"
	"testing"
)

func TestCodeBlockJSONRoundTrip(t *testing.T) {
	want := CodeBlock{Index: 1, Language: LanguagePython, Code: "print('RESULT')"}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got CodeBlock
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("block = %#v", got)
	}
}
