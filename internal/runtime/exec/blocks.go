package exec

type Language string

const (
	LanguagePython Language = "python"
	LanguageShell  Language = "shell"
)

type CodeBlock struct {
	Index    int      `json:"index"`
	Language Language `json:"language"`
	Code     string   `json:"code"`
}
