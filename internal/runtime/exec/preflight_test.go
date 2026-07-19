package exec

import (
	"strings"
	"testing"
)


func TestPreflightRejectsNonExecutablePython(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{name: "json", code: `{"url":"https://example.com"}`, want: "JSON"},
		{name: "print only", code: `print("hello")`, want: "print"},
		{name: "placeholder", code: `print(TARGET)`, want: "placeholder"},
		{name: "syntax", code: `if True print("broken")`, want: "syntax"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Preflight(CodeBlock{Index: 1, Language: LanguagePython, Code: test.code})
			if result.Approved || !strings.Contains(strings.ToLower(result.Rejection), strings.ToLower(test.want)) {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestPreflightRepairsMissingImportsAndHTTPTimeout(t *testing.T) {
	code := "payload = base64.b64encode(b'x')\nparsed = urllib.parse.urlparse('https://example.com')\nresponse = requests.get(parsed.geturl())\nprint(response.status_code)\n"
	result := Preflight(CodeBlock{Index: 1, Language: LanguagePython, Code: code})
	if !result.Approved {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"import base64", "import urllib.parse", "timeout=15"} {
		if !strings.Contains(result.Code, want) {
			t.Fatalf("repaired code does not contain %q: %s", want, result.Code)
		}
	}
	if len(result.Repairs) != 3 || result.OriginalCode != code {
		t.Fatalf("result = %+v", result)
	}
}

func TestPreflightLeavesShellUntouched(t *testing.T) {
	code := "curl -sS \"$PENTGO_TARGET\"\n"
	result := Preflight(CodeBlock{Index: 1, Language: LanguageShell, Code: code})
	if !result.Approved || result.Code != code || len(result.Repairs) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
