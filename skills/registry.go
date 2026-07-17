package skills

import (
	_ "embed"
	"fmt"
	"sort"
)

const maxSkillBytes = 16000

//go:embed recon/SKILL.md
var reconPrompt string

//go:embed terminal/SKILL.md
var terminalPrompt string

//go:embed waf-bypass/SKILL.md
var wafBypassPrompt string

//go:embed nosql-injection/SKILL.md
var nosqlInjectionPrompt string

//go:embed type-juggling/SKILL.md
var typeJugglingPrompt string

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
	"waf-bypass": {
		description: "WAF 绕过技法：编码/大小写/注释/分块/HTTP 层面的检测规避手法。",
		content:     wafBypassPrompt,
	},
	"nosql-injection": {
		description: "NoSQL 注入：MongoDB/Redis 等运算符注入、认证绕过与盲注提取。",
		content:     nosqlInjectionPrompt,
	},
	"type-juggling": {
		description: "类型混淆：PHP 松散比较、魔术哈希与 JSON 类型强制导致的认证/逻辑绕过。",
		content:     typeJugglingPrompt,
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
