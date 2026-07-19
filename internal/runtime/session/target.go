package session

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var targetPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'，。；、]+|(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}(?::[0-9]{1,5})?(?:/[^\s<>"'，。；、]+)?`)

// Target 保存用户原始目标文本与规范化 HTTP(S) 地址。
type Target struct {
	Raw       string `json:"raw"`
	Canonical string `json:"canonical"`
}

// ParseTarget 从自然语言任务中提取第一个 HTTP(S) URL 或裸域名。
func ParseTarget(task string) (Target, error) {
	raw := targetPattern.FindString(task)
	if raw == "" {
		return Target{}, fmt.Errorf("task does not contain an HTTP(S) target")
	}
	parseInput := raw
	if !strings.Contains(parseInput, "://") {
		parseInput = "https://" + parseInput
	}
	parsed, err := url.Parse(parseInput)
	if err != nil {
		return Target{}, fmt.Errorf("parse target: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Target{}, fmt.Errorf("unsupported target scheme %q", parsed.Scheme)
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return Target{}, fmt.Errorf("target host is empty")
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if strings.Contains(hostname, ":") {
		parsed.Host = net.JoinHostPort(hostname, port)
		if port == "" {
			parsed.Host = "[" + hostname + "]"
		}
	} else if port == "" {
		parsed.Host = hostname
	} else {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.User = nil
	parsed.Fragment = ""
	return Target{Raw: raw, Canonical: parsed.String()}, nil
}
