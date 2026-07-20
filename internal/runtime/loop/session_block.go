package loop

import (
	"regexp"
	"strings"

	sess "pentgo/internal/runtime/session"
	"pentgo/internal/runtime/verify"
)

var sessionBlockPattern = regexp.MustCompile(`(?s)=== PENTGO SESSION ===\s*(.*?)\s*=== END PENTGO SESSION ===`)

// SessionSpec is a model-declared engagement login identity.
type SessionSpec struct {
	Name             string
	Role             string
	Username         string
	LoginURL         string
	LoginMethod      string
	LoginBody        string
	LoginContentType string
}

// ParseSessionSpecs extracts PENTGO SESSION blocks from model text.
// Invalid names or missing login_url are dropped.
func ParseSessionSpecs(text string) []SessionSpec {
	matches := sessionBlockPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var specs []SessionSpec
	for _, match := range matches {
		spec := parseSessionSpec(match[1])
		if strings.TrimSpace(spec.LoginURL) == "" {
			continue
		}
		name := sess.SessionNameFromIdentity(spec.Name, spec.Username, "")
		if name == "" {
			continue
		}
		spec.Name = name
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.TrimSpace(spec.LoginMethod) == "" {
			spec.LoginMethod = "POST"
		}
		if strings.TrimSpace(spec.LoginContentType) == "" {
			spec.LoginContentType = "application/x-www-form-urlencoded"
		}
		specs = append(specs, spec)
	}
	return specs
}

func parseSessionSpec(block string) SessionSpec {
	var spec SessionSpec
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			spec.Name = value
		case "role":
			spec.Role = value
		case "username":
			spec.Username = value
		case "login_url":
			spec.LoginURL = value
		case "login_method":
			spec.LoginMethod = value
		case "login_body":
			spec.LoginBody = value
		case "login_content_type":
			spec.LoginContentType = value
		}
	}
	return spec
}

func (spec SessionSpec) toLoginSpec() verify.LoginSpec {
	return verify.LoginSpec{
		LoginURL:         spec.LoginURL,
		LoginMethod:      spec.LoginMethod,
		LoginBody:        spec.LoginBody,
		LoginContentType: spec.LoginContentType,
	}
}
