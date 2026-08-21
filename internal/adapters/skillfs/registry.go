package skillfs

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// maxSkillBytes 限制单个注入技能正文的大小，以保护模型上下文容量。
const maxSkillBytes = 32000

// Skill 是一份 Markdown 文档在提示词中可见的目录条目。
type Skill struct {
	Name        string
	Description string
}

// Registry 保存扫描得到的目录，并按稳定名称加载正文。
type Registry struct {
	mu      sync.RWMutex
	source  fs.FS
	ready   bool
	catalog []Skill
	paths   map[string]string
}

// NewRegistry 将注册表绑定到 source；读取前必须先执行 Scan。
func NewRegistry(source fs.FS) *Registry {
	return &Registry{source: source}
}

// Loaded 判断成功的 Scan 是否已填充目录。
func (registry *Registry) Loaded() bool {
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.ready
}

// Scan 仅读取顶层 Markdown 元数据，并返回注入提示词的摘要。
// 完整正文会保持未加载状态，直至调用 Load。
func (registry *Registry) Scan() (string, error) {
	if registry == nil || registry.source == nil {
		return "", fmt.Errorf("skill registry filesystem is nil")
	}
	paths, err := fs.Glob(registry.source, "*.md")
	if err != nil {
		return "", fmt.Errorf("scan skills: %w", err)
	}
	catalog := make([]Skill, 0, len(paths))
	registered := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := fs.ReadFile(registry.source, path)
		if err != nil {
			return "", fmt.Errorf("read skill %s: %w", path, err)
		}
		frontmatter, body, err := parseDocument(content)
		if err != nil {
			return "", fmt.Errorf("parse skill %s: %w", path, err)
		}
		name := strings.TrimSuffix(path, ".md")
		description := strings.TrimSpace(frontmatter.Description)
		if description == "" {
			description = heading(name, body)
		}
		catalog = append(catalog, Skill{Name: name, Description: description})
		registered[name] = path
	}
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	registry.mu.Lock()
	registry.catalog = catalog
	registry.paths = registered
	registry.ready = true
	registry.mu.Unlock()
	return registry.Summary()
}

// Catalog 返回按名称排序的已扫描技能元数据副本。
func (registry *Registry) Catalog() []Skill {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return append([]Skill(nil), registry.catalog...)
}

// Names 返回 Load 可接受的排序后目录标识。
func (registry *Registry) Names() []string {
	catalog := registry.Catalog()
	names := make([]string, 0, len(catalog))
	for _, skill := range catalog {
		names = append(names, skill.Name)
	}
	return names
}

// Summary 渲染供模型发现技能的目录元数据，但不包含技能正文。
func (registry *Registry) Summary() (string, error) {
	if registry == nil {
		return "", fmt.Errorf("skill registry is nil")
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.ready {
		return "", fmt.Errorf("skills are not loaded; run /load_skill first")
	}
	var builder strings.Builder
	builder.WriteString("## 可用 PentGo 技能\n\n")
	for _, skill := range registry.catalog {
		fmt.Fprintf(&builder, "- `%s`：%s\n", skill.Name, skill.Description)
	}
	return bound([]byte(builder.String())), nil
}

// Load 在确认文档存在于扫描结果后，返回大小受限的文档正文。
func (registry *Registry) Load(name string) (string, error) {
	if registry == nil {
		return "", fmt.Errorf("skill registry is nil")
	}
	name = strings.TrimSpace(name)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.ready || registry.source == nil {
		return "", fmt.Errorf("skills are not loaded; run /load_skill first")
	}
	path, ok := registry.paths[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	content, err := fs.ReadFile(registry.source, path)
	if err != nil {
		return "", fmt.Errorf("load skill %q: %w", name, err)
	}
	_, body, err := parseDocument(content)
	if err != nil {
		return "", fmt.Errorf("parse skill %q: %w", name, err)
	}
	return bound(body), nil
}

// frontmatter 是从技能文档中提取的可选 YAML 元数据。
type frontmatter struct {
	Description string `yaml:"description"`
}

// parseDocument 在存在 YAML frontmatter 时将其与 Markdown 正文分离。
func parseDocument(content []byte) (frontmatter, []byte, error) {
	text := strings.TrimSpace(string(content))
	if !strings.HasPrefix(text, "---") || (len(text) > 3 && text[3] != '\n' && text[3] != '\r') {
		return frontmatter{}, []byte(text), nil
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return frontmatter{}, nil, fmt.Errorf("frontmatter closing delimiter not found")
	}
	var metadata frontmatter
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &metadata); err != nil {
		return frontmatter{}, []byte(strings.TrimSpace(rest[end+len("\n---"):])), nil
	}
	return metadata, []byte(strings.TrimSpace(rest[end+len("\n---"):])), nil
}

// heading 选取第一个 Markdown 标题，缺失时回退至文件名。
func heading(name string, content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			value := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if value != "" {
				return value
			}
		}
	}
	return name
}

// bound 截断超出大小限制的文档正文，并添加可见标记。
func bound(content []byte) string {
	if len(content) > maxSkillBytes {
		content = content[:maxSkillBytes]
	}
	return string(content)
}
