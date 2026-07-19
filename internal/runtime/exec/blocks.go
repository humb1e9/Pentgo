package exec

import (
	"regexp"
	"strings"
)

var fencedCodePattern = regexp.MustCompile("(?ms)^```([A-Za-z0-9_-]+)[ \\t]*\\r?\\n(.*?)^```[ \\t]*$")

// Language 是可由 Runtime 执行的代码块语言。
type Language string

const (
	LanguagePython Language = "python"
	LanguageShell  Language = "shell"
)

// CodeBlock 是模型回复中的一个支持执行的 fenced code block。
type CodeBlock struct {
	Index    int      `json:"index"`
	Language Language `json:"language"`
	Code     string   `json:"code"`
}

// ExtractCodeBlocks 以模型文本中的源码顺序提取全部支持代码块。
func ExtractCodeBlocks(response string) []CodeBlock {
	matches := fencedCodePattern.FindAllStringSubmatch(response, -1)
	blocks := make([]CodeBlock, 0, len(matches))
	for _, match := range matches {
		language, ok := supportedLanguage(match[1])
		if !ok {
			continue
		}
		blocks = append(blocks, CodeBlock{
			Index:    len(blocks) + 1,
			Language: language,
			Code:     match[2],
		})
	}
	return blocks
}

func supportedLanguage(value string) (Language, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "python", "python3":
		return LanguagePython, true
	case "bash", "sh", "shell":
		return LanguageShell, true
	default:
		return "", false
	}
}
