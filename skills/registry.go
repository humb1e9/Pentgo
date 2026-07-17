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

type skillEntry struct {
	description string
	content     string
}

var registered = map[string]skillEntry{
	"recon": {
		description: "信息收集方法论：以已回灌的执行输出为依据，逐步减少目标未知信息。",
		content:     reconPrompt,
	},
	"terminal": {
		description: "终端 Agent 通用准则：只把已执行并回灌的输出当作证据。",
		content:     terminalPrompt,
	},
}

// Skill 是可加载 Skill 的目录条目，不含正文。
type Skill struct {
	Name        string
	Description string
}

// Catalog 返回按名称升序排列的可加载 Skill 目录（名称与描述，不含正文）。
func Catalog() []Skill {
	catalog := make([]Skill, 0, len(registered))
	for name, entry := range registered {
		catalog = append(catalog, Skill{Name: name, Description: entry.description})
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	return catalog
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
	entry, ok := registered[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	if len(entry.content) > maxSkillBytes {
		return entry.content[:maxSkillBytes], nil
	}
	return entry.content, nil
}
