package tools

import (
	"testing"
	"testing/fstest"
)

func TestMatchPreloadsOnlyUniqueSpecificSkill(t *testing.T) {
	registry := NewRegistry(fstest.MapFS{
		"sqli-sql-injection.md": {Data: []byte("---\ndescription: SQL injection testing playbook.\n---\n# SQLi")},
		"api.md":                {Data: []byte("---\ndescription: API routing playbook.\n---\n# API")},
	})
	registry.Scan()

	skill, ok := registry.Match("请检查 SQL 注入漏洞")
	if !ok || skill.Name != "sqli-sql-injection" {
		t.Fatalf("specific skill match = %#v, %v", skill, ok)
	}
	if _, ok := registry.Match("检查 API"); ok {
		t.Fatal("generic API request should not auto-preload a skill")
	}
}
