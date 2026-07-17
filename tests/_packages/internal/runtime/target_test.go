package runtime

import "testing"

func TestParseTargetExtractsAndNormalizesURLFromTask(t *testing.T) {
	target, err := ParseTarget("检查 HTTPS://Example.COM:443/login?next=%2Fhome 的响应")
	if err != nil {
		t.Fatal(err)
	}
	if target.Raw != "HTTPS://Example.COM:443/login?next=%2Fhome" {
		t.Fatalf("raw = %q", target.Raw)
	}
	if target.Canonical != "https://example.com/login?next=%2Fhome" {
		t.Fatalf("canonical = %q", target.Canonical)
	}
}

func TestParseTargetAddsHTTPSForBareDomain(t *testing.T) {
	target, err := ParseTarget("收集 example.com/api 的公开信息")
	if err != nil {
		t.Fatal(err)
	}
	if target.Canonical != "https://example.com/api" {
		t.Fatalf("canonical = %q", target.Canonical)
	}
}

func TestParseTargetRejectsTaskWithoutHTTPSTarget(t *testing.T) {
	if _, err := ParseTarget("分析本地日志"); err == nil {
		t.Fatal("ParseTarget() error = nil")
	}
}
