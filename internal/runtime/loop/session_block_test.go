package loop

import (
	"strings"
	"testing"
)

func TestParseSessionDeclarationsAcceptsCompleteTopLevelBlocks(t *testing.T) {
	text := `
=== PENTGO SESSION ===
name: user_a
role: user
username: alice
login_url: https://target.example/login
login_method: post
login_body: username=alice&password=fixture-secret
login_content_type: application/x-www-form-urlencoded
=== END PENTGO SESSION ===
=== PENTGO SESSION ===
name: user_b
login_url: https://target.example/login
login_method: GET
login_body: ticket=fixture
login_content_type: application/x-www-form-urlencoded
=== END PENTGO SESSION ===
`
	result := ParseSessionDeclarations(text)
	if result.HasViolations() || len(result.Specs) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Specs[0].Name != "user_a" || result.Specs[0].LoginMethod != "POST" || result.Specs[1].LoginMethod != "GET" {
		t.Fatalf("specs = %+v", result.Specs)
	}
}

func TestParseSessionDeclarationsRejectsInvalidProtocol(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"missing name", sessionBlock("username: alice\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded"), "missing_session_field: name"},
		{"invalid name", sessionBlock("name: bad-name\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded"), "invalid_session_name"},
		{"invalid method", sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: PATCH\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded"), "invalid_login_method"},
		{"unknown field", sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded\ncsrf: fixture"), "unknown_session_field: csrf"},
		{"duplicate field", sessionBlock("name: user_a\nname: user_b\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded"), "duplicate_session_field: name"},
		{"malformed line", sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded\nbroken"), "malformed_session_line"},
		{"unclosed", "=== PENTGO SESSION ===\nname: user_a", "unclosed_session_block"},
		{"unexpected end", "=== END PENTGO SESSION ===", "unexpected_session_end"},
		{"inside fence", "```python\n=== PENTGO SESSION ===\nname: user_a\n=== END PENTGO SESSION ===\n```", "session_block_inside_code"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ParseSessionDeclarations(test.text)
			if !result.HasViolations() || len(result.Specs) != 0 || !strings.Contains(RenderSessionProtocolCorrection(result), test.want) {
				t.Fatalf("result/correction = %+v/%q", result, RenderSessionProtocolCorrection(result))
			}
		})
	}
}

func TestParseSessionDeclarationsRejectsDuplicateNamesAtomically(t *testing.T) {
	text := sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=one\nlogin_content_type: application/x-www-form-urlencoded") + "\n" + sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: password=two\nlogin_content_type: application/x-www-form-urlencoded")
	result := ParseSessionDeclarations(text)
	if len(result.Specs) != 0 || !strings.Contains(RenderSessionProtocolCorrection(result), "duplicate_session_name") {
		t.Fatalf("result = %+v", result)
	}
}

func TestRenderSessionProtocolCorrectionDoesNotLeakDeclarationValues(t *testing.T) {
	result := ParseSessionDeclarations(sessionBlock("name: bad-name\nlogin_url: https://target.example/private-login\nlogin_method: PATCH\nlogin_body: username=alice&password=fixture-secret\nlogin_content_type: application/x-www-form-urlencoded"))
	correction := RenderSessionProtocolCorrection(result)
	for _, secret := range []string{"fixture-secret", "private-login", "bad-name"} {
		if strings.Contains(correction, secret) {
			t.Fatalf("correction leaked %q: %s", secret, correction)
		}
	}
}

func TestParseSessionSpecsReturnsOnlyStrictlyValidDeclarations(t *testing.T) {
	text := sessionBlock("name: user_a\nlogin_url: https://target.example/login\nlogin_method: POST\nlogin_body: username=alice&password=secret\nlogin_content_type: application/x-www-form-urlencoded")
	specs := ParseSessionSpecs(text)
	if len(specs) != 1 || specs[0].Name != "user_a" {
		t.Fatalf("specs = %+v", specs)
	}
}

func sessionBlock(body string) string {
	return "=== PENTGO SESSION ===\n" + body + "\n=== END PENTGO SESSION ==="
}
