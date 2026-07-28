package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	placeholderPattern  = regexp.MustCompile(`\b(TARGET|HOST|TOKEN|OFFSET|PATCH_BYTE|PAYLOAD|SERIAL)\b`)
	printOnlyPattern    = regexp.MustCompile(`^print\s*\(`)
	requestsCallPattern = regexp.MustCompile(`requests\.(?:get|post|put|delete|patch|head|options|request)\s*\(`)
)

// PreflightResult 是代码执行前的可审计检查与修复结果。
type PreflightResult struct {
	Block        CodeBlock `json:"block"`
	OriginalCode string    `json:"original_code"`
	Code         string    `json:"code"`
	Repairs      []string  `json:"repairs,omitempty"`
	Approved     bool      `json:"approved"`
	Rejection    string    `json:"rejection,omitempty"`
}

// Preflight 检查 Python 代码并对有限缺失项产生修复副本。Shell 代码不改写。
func Preflight(block CodeBlock) PreflightResult {
	result := PreflightResult{Block: block, OriginalCode: block.Code, Code: block.Code}
	if block.Language == LanguageShell {
		result.Approved = true
		return result
	}
	if block.Language != LanguagePython {
		result.Rejection = "unsupported language"
		return result
	}
	if isJSONDocument(block.Code) {
		result.Rejection = "Python block is JSON, not executable code"
		return result
	}
	if reason := rejectPythonStub(block.Code); reason != "" {
		result.Rejection = reason
		return result
	}
	if placeholderPattern.MatchString(block.Code) {
		result.Rejection = "Python block contains an unresolved placeholder"
		return result
	}
	result.Code, result.Repairs = repairPython(block.Code)
	if err := compilePython(result.Code); err != nil {
		result.Rejection = fmt.Sprintf("Python syntax preflight failed: %v", err)
		return result
	}
	result.Approved = true
	return result
}

func isJSONDocument(code string) bool {
	trimmed := strings.TrimSpace(code)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}
	var value any
	return json.Unmarshal([]byte(trimmed), &value) == nil
}

func rejectPythonStub(code string) string {
	lines := strings.Split(code, "\n")
	executable := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		executable = append(executable, line)
	}
	if len(executable) == 0 {
		return "Python block is empty"
	}
	if len(executable) == 1 && (printOnlyPattern.MatchString(executable[0]) || executable[0] == "pass" || executable[0] == "...") {
		return "Python block has only print or placeholder code"
	}
	allImports := true
	for _, line := range executable {
		if !strings.HasPrefix(line, "import ") && !strings.HasPrefix(line, "from ") {
			allImports = false
			break
		}
	}
	if allImports {
		return "Python block has imports but no operation"
	}
	return ""
}

func repairPython(code string) (string, []string) {
	repairs := make([]string, 0, 3)
	prefix := make([]string, 0, 2)
	if strings.Contains(code, "base64.") && !hasPythonImport(code, "base64") {
		prefix = append(prefix, "import base64")
		repairs = append(repairs, "added import base64")
	}
	if strings.Contains(code, "urllib.parse.") && !hasPythonImport(code, "urllib.parse") {
		prefix = append(prefix, "import urllib.parse")
		repairs = append(repairs, "added import urllib.parse")
	}
	if strings.Contains(code, "requests.") {
		var repaired bool
		code, repaired = addRequestsTimeout(code)
		if repaired {
			repairs = append(repairs, "added requests timeout=15")
		}
	}
	if len(prefix) > 0 {
		code = strings.Join(prefix, "\n") + "\n" + code
	}
	return code, repairs
}

func hasPythonImport(code, module string) bool {
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(line)
		if line == "import "+module || strings.HasPrefix(line, "import "+module+" ") || strings.HasPrefix(line, "import "+module+",") {
			return true
		}
		if module == "urllib.parse" && strings.HasPrefix(line, "from urllib import parse") {
			return true
		}
		if module == "base64" && strings.HasPrefix(line, "from base64 import ") {
			return true
		}
	}
	return false
}

func addRequestsTimeout(code string) (string, bool) {
	lines := strings.SplitAfter(code, "\n")
	changed := false
	for index, line := range lines {
		if !strings.Contains(line, "requests.") || strings.Contains(line, "timeout=") {
			continue
		}
		matches := requestsCallPattern.FindAllStringIndex(line, -1)
		for matchIndex := len(matches) - 1; matchIndex >= 0; matchIndex-- {
			opening := matches[matchIndex][1] - 1
			closing := matchingCallParen(line, opening)
			if closing < 0 {
				continue
			}
			separator := ", "
			if strings.TrimSpace(line[opening+1:closing]) == "" {
				separator = ""
			}
			line = line[:closing] + separator + "timeout=15" + line[closing:]
			changed = true
		}
		lines[index] = line
	}
	return strings.Join(lines, ""), changed
}

// matchingCallParen returns the closing parenthesis belonging to opening. It
// deliberately scans one physical line only: multi-line calls are left intact.
func matchingCallParen(line string, opening int) int {
	if opening < 0 || opening >= len(line) || line[opening] != '(' {
		return -1
	}
	depth := 0
	var quote byte
	escaped := false
	for index := opening; index < len(line); index++ {
		character := line[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func compilePython(code string) error {
	command := exec.Command("python3", "-c", "import sys; compile(sys.stdin.read(), '<agent>', 'exec')")
	command.Stdin = strings.NewReader(code)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if len(output) == 0 {
		return err
	}
	return fmt.Errorf("%s", bytes.TrimSpace(output))
}
