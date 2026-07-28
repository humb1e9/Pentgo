package loop

import (
	"fmt"
	"strings"

	sess "pentgo/internal/runtime/session"
	"pentgo/internal/runtime/verify"
)

const (
	sessionStartMarker = "=== PENTGO SESSION ==="
	sessionEndMarker   = "=== END PENTGO SESSION ==="
)

var requiredSessionFields = []string{
	"name",
	"login_url",
	"login_method",
	"login_body",
	"login_content_type",
}

var allowedSessionFields = map[string]bool{
	"name":               true,
	"role":               true,
	"username":           true,
	"login_url":          true,
	"login_method":       true,
	"login_body":         true,
	"login_content_type": true,
}

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

// SessionProtocolError is a secret-free violation of the SESSION declaration grammar.
type SessionProtocolError struct {
	Code  string
	Field string
}

// SessionDeclarationResult contains valid declarations and all protocol violations.
type SessionDeclarationResult struct {
	Specs  []SessionSpec
	Errors []SessionProtocolError
}

// HasViolations reports whether this response must be rejected before execution.
func (result SessionDeclarationResult) HasViolations() bool {
	return len(result.Errors) > 0
}

// ParseSessionDeclarations strictly analyzes top-level PENTGO SESSION declarations.
func ParseSessionDeclarations(text string) SessionDeclarationResult {
	var result SessionDeclarationResult
	seenNames := make(map[string]bool)
	inFence := false
	inSession := false
	var lines []string

	finish := func() {
		spec, errors := parseSessionDeclaration(lines)
		result.Errors = append(result.Errors, errors...)
		if len(errors) != 0 {
			return
		}
		if seenNames[spec.Name] {
			result.Errors = append(result.Errors, SessionProtocolError{Code: "duplicate_session_name"})
			return
		}
		seenNames[spec.Name] = true
		result.Specs = append(result.Specs, spec)
	}

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if trimmed == sessionStartMarker || trimmed == sessionEndMarker {
				result.Errors = append(result.Errors, SessionProtocolError{Code: "session_block_inside_code"})
			}
			continue
		}
		switch trimmed {
		case sessionStartMarker:
			if inSession {
				result.Errors = append(result.Errors, SessionProtocolError{Code: "nested_session_block"})
				continue
			}
			inSession = true
			lines = nil
		case sessionEndMarker:
			if !inSession {
				result.Errors = append(result.Errors, SessionProtocolError{Code: "unexpected_session_end"})
				continue
			}
			finish()
			inSession = false
			lines = nil
		default:
			if inSession {
				lines = append(lines, line)
			}
		}
	}
	if inSession {
		result.Errors = append(result.Errors, SessionProtocolError{Code: "unclosed_session_block"})
	}
	if result.HasViolations() {
		result.Specs = nil
	}
	return result
}

func parseSessionDeclaration(lines []string) (SessionSpec, []SessionProtocolError) {
	values := make(map[string]string)
	var errors []SessionProtocolError
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !ok || key == "" {
			errors = append(errors, SessionProtocolError{Code: "malformed_session_line"})
			continue
		}
		if !allowedSessionFields[key] {
			errors = append(errors, SessionProtocolError{Code: "unknown_session_field", Field: key})
			continue
		}
		if _, exists := values[key]; exists {
			errors = append(errors, SessionProtocolError{Code: "duplicate_session_field", Field: key})
			continue
		}
		if value == "" {
			errors = append(errors, SessionProtocolError{Code: "empty_session_field", Field: key})
			continue
		}
		values[key] = value
	}
	for _, field := range requiredSessionFields {
		if values[field] == "" {
			errors = append(errors, SessionProtocolError{Code: "missing_session_field", Field: field})
		}
	}
	name := sess.NormalizeSessionName(values["name"])
	if values["name"] != "" && name == "" {
		errors = append(errors, SessionProtocolError{Code: "invalid_session_name"})
	}
	method := strings.ToUpper(values["login_method"])
	if values["login_method"] != "" && method != "POST" && method != "GET" {
		errors = append(errors, SessionProtocolError{Code: "invalid_login_method"})
	}
	return SessionSpec{
		Name:             name,
		Role:             values["role"],
		Username:         values["username"],
		LoginURL:         values["login_url"],
		LoginMethod:      method,
		LoginBody:        values["login_body"],
		LoginContentType: values["login_content_type"],
	}, errors
}

// ParseSessionSpecs returns only fully valid declarations for compatibility with callers.
func ParseSessionSpecs(text string) []SessionSpec {
	return ParseSessionDeclarations(text).Specs
}

// RenderSessionProtocolCorrection gives the model a stable, secret-free repair instruction.
func RenderSessionProtocolCorrection(result SessionDeclarationResult) string {
	var builder strings.Builder
	builder.WriteString("=== PENTGO SESSION PROTOCOL ERROR ===\n")
	builder.WriteString("No session was established and no code was run for this response.\nviolations:\n")
	for _, violation := range result.Errors {
		builder.WriteString("- ")
		builder.WriteString(violation.Code)
		if violation.Field != "" {
			builder.WriteString(": ")
			builder.WriteString(violation.Field)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("Use a top-level declaration outside every fenced code block. Required fields are: name, login_url, login_method, login_body, login_content_type. Optional fields are: role, username. login_method must be POST or GET.\n")
	builder.WriteString("The framework performs and verifies the login. Declare only login details present in returned CTF fixture evidence. Do not log in in code, print or copy cookies/tokens, or claim a session is available before a SESSION RESULT confirms it.\n")
	builder.WriteString("=== END PENTGO SESSION PROTOCOL ERROR ===")
	return builder.String()
}

func (spec SessionSpec) toLoginSpec() verify.LoginSpec {
	return verify.LoginSpec{
		LoginURL:         spec.LoginURL,
		LoginMethod:      spec.LoginMethod,
		LoginBody:        spec.LoginBody,
		LoginContentType: spec.LoginContentType,
	}
}

func (error SessionProtocolError) String() string {
	if error.Field == "" {
		return error.Code
	}
	return fmt.Sprintf("%s: %s", error.Code, error.Field)
}
