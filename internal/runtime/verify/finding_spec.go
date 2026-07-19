package verify

import (
	"regexp"
	"sort"
	"strings"
)

var findingSpecPattern = regexp.MustCompile(`(?s)=== PENTGO FINDING ===\s*(.*?)\s*=== END PENTGO FINDING ===`)

// ParseFindingSpecs parses model-declared framework verification requests.
// Unknown vulnerability types, malformed headers, and specs without a URL are
// ignored before they can reach the HTTP verifier.
func ParseFindingSpecs(text string) []FindingSpec {
	matches := findingSpecPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var specs []FindingSpec
	for _, match := range matches {
		spec := parseFindingSpec(match[1])
		if !knownVulnType(spec.VulnType) {
			continue
		}
		if spec.VulnType == VulnCredential {
			if strings.TrimSpace(spec.LoginURL) == "" {
				continue
			}
			spec.LoginMethod = normalizedLoginMethod(spec.LoginMethod)
			if !supportedVerificationMethod(spec.LoginMethod) {
				continue
			}
			if strings.TrimSpace(spec.LoginContentType) == "" {
				spec.LoginContentType = "application/x-www-form-urlencoded"
			}
		} else if strings.TrimSpace(spec.URL) == "" {
			continue
		}
		if strings.TrimSpace(spec.LoginURLB) != "" {
			spec.LoginMethodB = normalizedLoginMethod(spec.LoginMethodB)
			if !supportedVerificationMethod(spec.LoginMethodB) {
				continue
			}
			if strings.TrimSpace(spec.LoginContentTypeB) == "" {
				spec.LoginContentTypeB = "application/x-www-form-urlencoded"
			}
		}
		if strings.TrimSpace(spec.LoginURL) != "" && spec.VulnType != VulnCredential {
			spec.LoginMethod = normalizedLoginMethod(spec.LoginMethod)
			if strings.TrimSpace(spec.LoginContentType) == "" {
				spec.LoginContentType = "application/x-www-form-urlencoded"
			}
		}
		spec.Method = normalizedHTTPMethod(spec.Method)
		if !supportedVerificationMethod(spec.Method) {
			continue
		}
		key := findingSpecKey(spec)
		if seen[key] {
			continue
		}
		seen[key] = true
		specs = append(specs, spec)
	}
	return specs
}

func parseFindingSpec(block string) FindingSpec {
	var spec FindingSpec
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "type":
			spec.VulnType = VulnType(strings.ToLower(value))
		case "severity":
			spec.Severity = value
		case "method":
			spec.Method = value
		case "url":
			spec.URL = value
		case "baseline_url":
			spec.BaselineURL = value
		case "body":
			spec.Body = value
		case "baseline_body":
			spec.BaselineBody = value
		case "payload":
			spec.Payload = value
		case "description":
			spec.Description = value
		case "login_url":
			spec.LoginURL = value
		case "login_method":
			spec.LoginMethod = value
		case "login_body":
			spec.LoginBody = value
		case "login_content_type":
			spec.LoginContentType = value
		case "username":
			spec.Username = value
		case "login_url_b":
			spec.LoginURLB = value
		case "login_method_b":
			spec.LoginMethodB = value
		case "login_body_b":
			spec.LoginBodyB = value
		case "login_content_type_b":
			spec.LoginContentTypeB = value
		case "username_b":
			spec.UsernameB = value
		case "header":
			header, headerValue, valid := strings.Cut(value, ":")
			if !valid || strings.TrimSpace(header) == "" {
				continue
			}
			if spec.Headers == nil {
				spec.Headers = make(map[string]string)
			}
			spec.Headers[strings.TrimSpace(header)] = strings.TrimSpace(headerValue)
		}
	}
	return spec
}

func knownVulnType(vulnType VulnType) bool {
	switch vulnType {
	case VulnSQLI, VulnXSS, VulnLFI, VulnRCE, VulnAuthBypass, VulnCredential, VulnIDOR, VulnUpload, VulnOpenRedirect:
		return true
	default:
		return false
	}
}

func findingSpecKey(spec FindingSpec) string {
	keys := make([]string, 0, len(spec.Headers))
	for key := range spec.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{
		string(spec.VulnType),
		spec.Method,
		spec.URL,
		spec.BaselineURL,
		spec.Body,
		spec.BaselineBody,
		spec.Payload,
		spec.Severity,
		spec.Description,
		spec.LoginURL,
		spec.LoginMethod,
		spec.LoginBody,
		spec.LoginContentType,
		spec.Username,
	}
	for _, key := range keys {
		parts = append(parts, key, spec.Headers[key])
	}
	return strings.Join(parts, "\x00")
}

func normalizedLoginMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return "POST"
	}
	return method
}
