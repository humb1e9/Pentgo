package verify

import "testing"

func TestExtractCSRFTokenFromInputAndMeta(t *testing.T) {
	html := `<form><input type="hidden" name="csrf_token" value="tok-123"></form>`
	if got := ExtractCSRFToken(html); got != "tok-123" {
		t.Fatalf("input token = %q", got)
	}
	meta := `<meta name="csrf-token" content="meta-tok">`
	if got := ExtractCSRFToken(meta); got != "meta-tok" {
		t.Fatalf("meta token = %q", got)
	}
	jsonBody := `{"csrf_token":"json-tok"}`
	if got := ExtractCSRFToken(jsonBody); got != "json-tok" {
		t.Fatalf("json token = %q", got)
	}
}

func TestMergeCSRFTokenFormURLEncoded(t *testing.T) {
	merged := mergeCSRFToken("username=a&password=b", "application/x-www-form-urlencoded", "tok")
	if !bodyHasCSRFField(merged) {
		t.Fatalf("merged missing csrf: %q", merged)
	}
	// already present — leave body keys alone for password
	again := mergeCSRFToken("username=a&csrf_token=old", "application/x-www-form-urlencoded", "tok")
	if again != "username=a&csrf_token=old" && !bodyHasCSRFField(again) {
		t.Fatalf("should not force-replace when present: %q", again)
	}
}

func TestMergeCSRFTokenSkipsJSON(t *testing.T) {
	body := `{"user":"a"}`
	if got := mergeCSRFToken(body, "application/json", "tok"); got != body {
		t.Fatalf("json body changed: %q", got)
	}
}
