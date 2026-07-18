package runtime

import (
	"net/http"
	"testing"
)

func TestParseFindingSpecsParsesGETSpec(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: sqli
severity: high
method: GET
url: https://target.example/item?id=1%27
baseline_url: https://target.example/item?id=1
payload: id=1%27
description: id parameter SQL error
=== END PENTGO FINDING ===`)
	if len(specs) != 1 {
		t.Fatalf("spec count = %d", len(specs))
	}
	spec := specs[0]
	if spec.VulnType != VulnSQLI || spec.Method != http.MethodGet || spec.URL != "https://target.example/item?id=1%27" || spec.BaselineURL == "" || spec.Payload != "id=1%27" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestParseFindingSpecsParsesPOSTBodyAndHeaders(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: upload
method: POST
url: https://target.example/upload
body: file=payload.txt
baseline_body: file=benign.txt
payload: file=payload.txt
header: Content-Type: application/x-www-form-urlencoded
header: X-Trace: test
description: upload check
=== END PENTGO FINDING ===`)
	if len(specs) != 1 {
		t.Fatalf("spec count = %d", len(specs))
	}
	spec := specs[0]
	if spec.Method != http.MethodPost || spec.Body != "file=payload.txt" || spec.BaselineBody != "file=benign.txt" || spec.Headers["Content-Type"] != "application/x-www-form-urlencoded" || spec.Headers["X-Trace"] != "test" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestParseFindingSpecsKeepsDistinctSpecsAndDeduplicates(t *testing.T) {
	text := `
=== PENTGO FINDING ===
type: xss
url: https://target.example/?q=one
payload: one
=== END PENTGO FINDING ===
=== PENTGO FINDING ===
type: xss
url: https://target.example/?q=one
payload: one
=== END PENTGO FINDING ===
=== PENTGO FINDING ===
type: xss
url: https://target.example/?q=two
payload: two
=== END PENTGO FINDING ===`
	specs := ParseFindingSpecs(text)
	if len(specs) != 2 {
		t.Fatalf("spec count = %d, specs = %+v", len(specs), specs)
	}
}

func TestParseFindingSpecsSkipsUnknownType(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: unknown
url: https://target.example/
=== END PENTGO FINDING ===`)
	if len(specs) != 0 {
		t.Fatalf("unknown type parsed: %+v", specs)
	}
}

func TestParseFindingSpecsSkipsMissingURL(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: sqli
payload: id=1%27
=== END PENTGO FINDING ===`)
	if len(specs) != 0 {
		t.Fatalf("missing URL parsed: %+v", specs)
	}
}

func TestParseFindingSpecsParsesCredentialLoginFields(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: credential
login_url: https://target.example/login
login_body: username=admin&password=admin
login_content_type: application/x-www-form-urlencoded
username: admin
payload: username=admin
description: default credentials accepted
=== END PENTGO FINDING ===`)
	if len(specs) != 1 {
		t.Fatalf("spec count = %d, specs = %+v", len(specs), specs)
	}
	spec := specs[0]
	if spec.VulnType != VulnCredential || spec.LoginURL != "https://target.example/login" || spec.LoginMethod != http.MethodPost || spec.LoginBody != "username=admin&password=admin" || spec.LoginContentType != "application/x-www-form-urlencoded" || spec.Username != "admin" {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestParseFindingSpecsSkipsCredentialWithoutLoginURL(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: credential
login_body: username=admin&password=admin
username: admin
=== END PENTGO FINDING ===`)
	if len(specs) != 0 {
		t.Fatalf("credential without login URL parsed: %+v", specs)
	}
}

func TestParseFindingSpecsKeepsNonCredentialWithoutLoginFields(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: xss
url: https://target.example/?q=payload
payload: payload
=== END PENTGO FINDING ===`)
	if len(specs) != 1 || specs[0].VulnType != VulnXSS || specs[0].LoginURL != "" {
		t.Fatalf("specs = %+v", specs)
	}
}

func TestParseFindingSpecsSkipsUnsupportedMethod(t *testing.T) {
	specs := ParseFindingSpecs(`
=== PENTGO FINDING ===
type: xss
method: GET; touch /tmp/pentgo
url: https://target.example/?q=payload
payload: payload
=== END PENTGO FINDING ===`)
	if len(specs) != 0 {
		t.Fatalf("unsupported method parsed: %+v", specs)
	}
}

func TestParseFindingSpecsReturnsNilWithoutBlocks(t *testing.T) {
	if specs := ParseFindingSpecs("no structured declarations"); specs != nil {
		t.Fatalf("specs = %+v", specs)
	}
}
