package loop

import "testing"

func TestParseSessionSpecs(t *testing.T) {
	text := `
=== PENTGO SESSION ===
name: user_a
role: user
username: alice
login_url: https://target.example/login
login_method: POST
login_body: username=alice&password=secret
=== END PENTGO SESSION ===
=== PENTGO SESSION ===
username: bob!
login_url: https://target.example/login
login_body: username=bob&password=x
=== END PENTGO SESSION ===
=== PENTGO SESSION ===
name: bad-name
login_url: https://target.example/login
login_body: a=b
=== END PENTGO SESSION ===
`
	specs := ParseSessionSpecs(text)
	if len(specs) != 1 {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[0].Name != "user_a" || specs[0].Username != "alice" || specs[0].LoginURL == "" {
		t.Fatalf("spec = %+v", specs[0])
	}
}

func TestParseSessionSpecsUsesUsernameAsName(t *testing.T) {
	text := `
=== PENTGO SESSION ===
username: alice
login_url: https://target.example/login
login_body: username=alice&password=x
=== END PENTGO SESSION ===
`
	specs := ParseSessionSpecs(text)
	if len(specs) != 1 || specs[0].Name != "alice" {
		t.Fatalf("specs = %+v", specs)
	}
}
