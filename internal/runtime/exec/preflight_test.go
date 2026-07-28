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

func TestPreflightAddsTimeoutWithoutBreakingRequestsShapes(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		contains    string
		notContains string
		repaired    bool
	}{
		{
			name:        "session constructor is unchanged",
			code:        "session = requests.Session()\nresponse = session.get(url)\nprint(response.status_code)\n",
			notContains: "timeout=15",
		},
		{
			name:     "chained json call",
			code:     "data = requests.get(url).json()\nprint(data)\n",
			contains: "requests.get(url, timeout=15).json()",
			repaired: true,
		},
		{
			name:        "multiline call is unchanged",
			code:        "response = requests.get(\n    url\n)\nprint(response.status_code)\n",
			notContains: "timeout=15",
		},
		{
			name:     "post with existing arguments",
			code:     "response = requests.post(url, data=payload)\nprint(response.status_code)\n",
			contains: "requests.post(url, data=payload, timeout=15)",
			repaired: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Preflight(CodeBlock{Index: 1, Language: LanguagePython, Code: test.code})
			if !result.Approved {
				t.Fatalf("result = %+v", result)
			}
			if test.contains != "" && !strings.Contains(result.Code, test.contains) {
				t.Fatalf("repaired code missing %q: %s", test.contains, result.Code)
			}
			if test.notContains != "" && strings.Contains(result.Code, test.notContains) {
				t.Fatalf("repaired code unexpectedly contains %q: %s", test.notContains, result.Code)
			}
			if got := strings.Contains(strings.Join(result.Repairs, "\n"), "requests timeout"); got != test.repaired {
				t.Fatalf("repairs = %+v, timeout repair = %v", result.Repairs, got)
			}
		})
	}
}

func TestPreflightLeavesShellUntouched(t *testing.T) {
	code := "curl -sS \"$PENTGO_TARGET\"\n"
	result := Preflight(CodeBlock{Index: 1, Language: LanguageShell, Code: code})
	if !result.Approved || result.Code != code || len(result.Repairs) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
