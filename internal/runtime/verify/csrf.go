package verify

import (
	"net/url"
	"regexp"
	"strings"
)

// CSRF patterns aligned with bingo session_manager.CsrfExtractor (no site hardcoding).
var csrfPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<input[^>]+name=["'](?:csrf[_\-]?token|_token|authenticity_token|__RequestVerificationToken)["'][^>]+value=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)<input[^>]+value=["']([^"']+)["'][^>]+name=["'](?:csrf[_\-]?token|_token|authenticity_token|__RequestVerificationToken)["']`),
	regexp.MustCompile(`(?i)<meta[^>]+name=["']csrf-token["'][^>]+content=["']([^"']+)["']`),
	regexp.MustCompile(`(?i)"csrf[_\-]?token"\s*:\s*"([^"]+)"`),
}

var csrfBodyKeys = []string{
	"csrf_token", "_token", "authenticity_token", "__RequestVerificationToken", "csrf",
}

// ExtractCSRFToken returns the first CSRF token found in HTML/JSON body text.
func ExtractCSRFToken(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	for _, pattern := range csrfPatterns {
		if match := pattern.FindStringSubmatch(body); len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

// bodyHasCSRFField reports whether form-urlencoded body already carries a csrf key.
func bodyHasCSRFField(body string) bool {
	values, err := url.ParseQuery(body)
	if err != nil {
		lower := strings.ToLower(body)
		for _, key := range csrfBodyKeys {
			if strings.Contains(lower, strings.ToLower(key)+"=") {
				return true
			}
		}
		return false
	}
	for _, key := range csrfBodyKeys {
		if values.Get(key) != "" {
			return true
		}
	}
	return false
}

// mergeCSRFToken injects a CSRF token into form-urlencoded bodies that lack one.
// Non-form bodies are returned unchanged (token still available on the outcome).
func mergeCSRFToken(body, contentType, token string) string {
	token = strings.TrimSpace(token)
	if token == "" || bodyHasCSRFField(body) {
		return body
	}
	if !strings.HasPrefix(strings.ToLower(normalizedLoginContentType(contentType)), "application/x-www-form-urlencoded") {
		return body
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		if strings.TrimSpace(body) == "" {
			return "csrf_token=" + url.QueryEscape(token)
		}
		return body + "&csrf_token=" + url.QueryEscape(token)
	}
	values.Set("csrf_token", token)
	values.Set("_token", token)
	return values.Encode()
}
