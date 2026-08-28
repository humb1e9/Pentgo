package agent

import (
	"net/url"
	"regexp"
	"strings"
)

var targetPattern = regexp.MustCompile(`(?i)https?://[^\s<>"',，。；、]+|(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}(?::[0-9]{1,5})?(?:/[^\s<>"',，。；、]*)?`)

func extractTargets(intent string) []string {
	seen, result := make(map[string]bool), make([]string, 0)
	for _, raw := range targetPattern.FindAllString(intent, -1) {
		candidate := raw
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		parsed.Scheme, parsed.Host, parsed.Fragment = strings.ToLower(parsed.Scheme), strings.ToLower(parsed.Host), ""
		if canonical := parsed.String(); !seen[canonical] {
			seen[canonical] = true
			result = append(result, canonical)
		}
	}
	return result
}
