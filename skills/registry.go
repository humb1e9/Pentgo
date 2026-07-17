package skills

import (
	"embed"
	"fmt"
	"sort"
)

const maxSkillBytes = 32000

//go:embed recon/SKILL.md
//go:embed terminal/SKILL.md
//go:embed waf-bypass/SKILL.md
//go:embed nosql-injection/SKILL.md
//go:embed type-juggling/SKILL.md
var skillFS embed.FS

// descriptions 是已登记 Skill 的唯一真源：名称 -> 目录清单描述。
var descriptions = map[string]string{
	"recon":           "信息收集方法论：以已回灌的执行输出为依据，逐步减少目标未知信息。",
	"terminal":        "终端 Agent 通用准则：只把已执行并回灌的输出当作证据。",
	"waf-bypass":      "WAF 绕过技法：编码/大小写/注释/分块/HTTP 层面的检测规避手法。",
	"nosql-injection": "NoSQL 注入：MongoDB/Redis 等运算符注入、认证绕过与盲注提取。",
	"type-juggling":   "类型混淆：PHP 松散比较、魔术哈希与 JSON 类型强制导致的认证/逻辑绕过。",
}

// Skill 是可加载 Skill 的目录条目，不含正文。
type Skill struct {
	Name        string
	Description string
}

// Catalog 返回按名称升序排列的可加载 Skill 目录（名称与描述，不含正文）。
func Catalog() []Skill {
	catalog := make([]Skill, 0, len(descriptions))
	for name, description := range descriptions {
		catalog = append(catalog, Skill{Name: name, Description: description})
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	return catalog
}

// Names 返回可由 Runtime 加载的 Skill 名称（升序）。
func Names() []string {
	names := make([]string, 0, len(descriptions))
	for name := range descriptions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Load 返回一个注册的只读 Skill，内容被限制为模型上下文上限。
// 先校验名在 descriptions 中，天然拒绝未知名与路径穿越（如 "../recon"）。
func Load(name string) (string, error) {
	if _, ok := descriptions[name]; !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	content, err := skillFS.ReadFile(name + "/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("load skill %q: %w", name, err)
	}
	if len(content) > maxSkillBytes {
		return string(content[:maxSkillBytes]), nil
	}
	return string(content), nil
}
