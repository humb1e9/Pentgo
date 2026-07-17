package runtime

import "testing"

func TestIsRefusal(t *testing.T) {
	refusals := []string{
		"I can't help with that.",
		"I'm unable to assist with hacking.",
		"抱歉，我无法协助进行渗透测试。",
		"I must decline this request.",
	}
	for _, text := range refusals {
		if !isRefusal(text) {
			t.Fatalf("expected refusal: %q", text)
		}
	}
	nonRefusals := []string{
		"I'll probe the endpoint now.",
		"Analysis complete, no issues found.",
		"```python\nprint('x')\n```",
	}
	for _, text := range nonRefusals {
		if isRefusal(text) {
			t.Fatalf("false refusal: %q", text)
		}
	}
}
