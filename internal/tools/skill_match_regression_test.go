package tools

import (
	"testing"
	"testing/fstest"
)

func TestMatchesPreloadsSpecificSkillsAndSkipsGenericRequests(t *testing.T) {
	registry := NewRegistry(fstest.MapFS{
		"sqli-sql-injection.md":               {Data: []byte("---\ndescription: SQL injection testing playbook.\n---\n# SQLi")},
		"ssrf-server-side-request-forgery.md": {Data: []byte("---\ndescription: SSRF testing playbook.\n---\n# SSRF")},
		"api.md":                              {Data: []byte("---\ndescription: API routing playbook.\n---\n# API")},
	})
	registry.Scan()

	matches := registry.Matches("请检查 SQL 注入和 SSRF 漏洞", 3)
	if len(matches) != 2 || matches[0].Name != "sqli-sql-injection" || matches[1].Name != "ssrf-server-side-request-forgery" {
		t.Fatalf("specific matches = %#v", matches)
	}
	if matches := registry.Matches("检查 API", 3); len(matches) != 0 {
		t.Fatalf("generic API request matched %#v", matches)
	}
}
