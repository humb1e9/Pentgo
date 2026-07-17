package skills

import (
	_ "embed"
	"fmt"
	"sort"
)

const maxSkillBytes = 4000

//go:embed recon/SKILL.md
var reconPrompt string

//go:embed terminal/SKILL.md
var terminalPrompt string

var registered = map[string]string{
	"recon":    reconPrompt,
	"terminal": terminalPrompt,
}

// Names 返回可由 Runtime 加载的 Skill 名称。
func Names() []string {
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Load 返回一个注册的只读 Skill，内容被限制为模型上下文上限。
func Load(name string) (string, error) {
	prompt, ok := registered[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	if len(prompt) > maxSkillBytes {
		return prompt[:maxSkillBytes], nil
	}
	return prompt, nil
}
