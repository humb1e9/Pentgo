package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// maxSkillBytes limits the size of a skill body injected into the model context.
const maxSkillBytes = 32 * 1024

// maxCatalogDescriptionBytes keeps a large skill catalog within a bounded session context.
const maxCatalogDescriptionBytes = 160

// Skill is the metadata for a Markdown document visible in the skill catalog.
type Skill struct {
	Name        string
	Description string
}

// Diagnostic describes a file or scan failure that did not abort a scan.
type Diagnostic struct {
	Path   string
	Reason string
}

// ScanResult is the complete result of a registry scan.
type ScanResult struct {
	Catalog     []Skill
	Diagnostics []Diagnostic
}

// ErrUnknownSkill is returned when a name was not present in the last scan.
var ErrUnknownSkill = errors.New("unknown skill")

// Registry stores scanned metadata and paths, but never full skill bodies.
type Registry struct {
	mu      sync.RWMutex
	source  fs.FS
	catalog []Skill
	paths   map[string]string
}

// NewRegistry binds a registry to source. Skills are discovered by Scan.
func NewRegistry(source fs.FS) *Registry {
	return &Registry{source: source, paths: make(map[string]string)}
}

// Scan reads Markdown frontmatter and atomically replaces all registry state.
// Individual file failures are recorded as diagnostics and do not stop scanning.
func (registry *Registry) Scan() ScanResult {
	result := ScanResult{}
	paths := make(map[string]string)
	if registry == nil || registry.source == nil {
		result.Diagnostics = []Diagnostic{{Path: "skills", Reason: "skill registry filesystem is unavailable"}}
		if registry != nil {
			registry.replace(result, paths)
		}
		return result
	}

	matches, err := fs.Glob(registry.source, "*.md")
	if err != nil {
		result.Diagnostics = []Diagnostic{{Path: "skills", Reason: fmt.Sprintf("glob skills: %v", err)}}
		registry.replace(result, paths)
		return result
	}

	catalog := make([]Skill, 0, len(matches))
	for _, path := range matches {
		content, err := fs.ReadFile(registry.source, path)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: path, Reason: fmt.Sprintf("read skill: %v", err)})
			continue
		}
		metadata, _, err := parseDocument(content)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: path, Reason: err.Error()})
			continue
		}
		description := normalizeDescription(metadata.Description)
		if description == "" {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Path: path, Reason: "frontmatter description is required"})
			continue
		}
		name := strings.TrimSuffix(path, ".md")
		catalog = append(catalog, Skill{Name: name, Description: description})
		paths[name] = path
	}
	slices.SortFunc(catalog, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	result.Catalog = catalog
	registry.replace(result, paths)
	return cloneScanResult(result)
}

func (registry *Registry) replace(result ScanResult, paths map[string]string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.catalog = append([]Skill(nil), result.Catalog...)
	registry.paths = make(map[string]string, len(paths))
	for name, path := range paths {
		registry.paths[name] = path
	}
}

func cloneScanResult(result ScanResult) ScanResult {
	result.Catalog = append([]Skill(nil), result.Catalog...)
	result.Diagnostics = append([]Diagnostic(nil), result.Diagnostics...)
	return result
}

// Matches returns up to limit relevant skills ranked by a conservative fuzzy score.
func (registry *Registry) Matches(request string, limit int) []Skill {
	request = strings.ToLower(strings.TrimSpace(request))
	if registry == nil || request == "" || limit <= 0 {
		return nil
	}
	type candidate struct {
		skill Skill
		score int
	}
	candidates := make([]candidate, 0)
	registry.mu.RLock()
	for _, skill := range registry.catalog {
		if score := skillMatchScore(request, skill); score >= 4 {
			candidates = append(candidates, candidate{skill: skill, score: score})
		}
	}
	registry.mu.RUnlock()
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.score == b.score {
			return strings.Compare(a.skill.Name, b.skill.Name)
		}
		return b.score - a.score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	matched := make([]Skill, len(candidates))
	for index, candidate := range candidates {
		matched[index] = candidate.skill
	}
	return matched
}

func skillMatchScore(request string, skill Skill) int {
	name := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(skill.Name, "-", " "), "_", " "))
	if utf8.RuneCountInString(name) >= 5 && strings.Contains(request, name) {
		return 100
	}
	description := strings.ToLower(skill.Description)
	best := 0
	for _, fragment := range chineseFragments(request) {
		if strings.Contains(description, fragment) && utf8.RuneCountInString(fragment) > best {
			best = utf8.RuneCountInString(fragment)
		}
	}
	for _, token := range strings.FieldsFunc(name, func(r rune) bool { return r == ' ' }) {
		if strings.Contains(request, token) {
			if len(token) >= 4 {
				best += 4
			} else if len(token) >= 3 {
				best += 3
			}
			continue
		}
		// Common security skill names use a trailing "i" abbreviation, such as
		// sqli. Match its stable root "sql" without treating generic short names
		// (for example api) as an automatic preload signal.
		root := strings.TrimSuffix(token, "i")
		if len(token) >= 4 && len(root) >= 3 && strings.Contains(request, root) {
			best += 4
		}
	}
	return best
}

func chineseFragments(text string) []string {
	var fragments []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r < 0x4e00 || r > 0x9fff }) {
		runes := []rune(part)
		for length := min(12, len(runes)); length >= 4; length-- {
			for start := 0; start+length <= len(runes); start++ {
				fragments = append(fragments, string(runes[start:start+length]))
			}
		}
	}
	return fragments
}

// Load lazily reads and validates a skill body from a path accepted by Scan.
func (registry *Registry) Load(name string) (string, error) {
	name = strings.TrimSpace(name)
	if registry == nil {
		return "", fmt.Errorf("%w: %q", ErrUnknownSkill, name)
	}
	registry.mu.RLock()
	source := registry.source
	path, ok := registry.paths[name]
	registry.mu.RUnlock()
	if !ok || source == nil {
		return "", fmt.Errorf("%w: %q", ErrUnknownSkill, name)
	}
	content, err := fs.ReadFile(source, path)
	if err != nil {
		return "", fmt.Errorf("load skill %q: %w", name, err)
	}
	metadata, body, err := parseDocument(content)
	if err != nil {
		return "", fmt.Errorf("parse skill %q: %w", name, err)
	}
	if normalizeDescription(metadata.Description) == "" {
		return "", fmt.Errorf("parse skill %q: description is required", name)
	}
	return bound(body), nil
}

type frontmatter struct {
	Description string `yaml:"description"`
}

// parseDocument requires strict YAML frontmatter and returns its stripped body.
func parseDocument(content []byte) (frontmatter, []byte, error) {
	text := string(content)
	firstEnd := strings.IndexByte(text, '\n')
	if firstEnd < 0 {
		if strings.TrimSuffix(text, "\r") != "---" {
			return frontmatter{}, nil, fmt.Errorf("frontmatter opening delimiter not found")
		}
		return frontmatter{}, nil, fmt.Errorf("frontmatter closing delimiter not found")
	}
	firstLine := strings.TrimSuffix(text[:firstEnd], "\r")
	if firstLine != "---" {
		return frontmatter{}, nil, fmt.Errorf("frontmatter opening delimiter not found")
	}

	rest := text[firstEnd+1:]
	position := 0
	for {
		lineEnd := strings.IndexByte(rest[position:], '\n')
		line := rest[position:]
		next := len(rest)
		if lineEnd >= 0 {
			line = rest[position : position+lineEnd]
			next = position + lineEnd + 1
		}
		if strings.TrimSuffix(line, "\r") == "---" {
			var metadata frontmatter
			yamlText := rest[:position]
			if err := yaml.Unmarshal([]byte(yamlText), &metadata); err != nil {
				return frontmatter{}, nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
			}
			metadata.Description = normalizeDescription(metadata.Description)
			if metadata.Description == "" {
				return frontmatter{}, nil, fmt.Errorf("frontmatter description is required")
			}
			body := rest[next:]
			return metadata, []byte(strings.TrimSpace(body)), nil
		}
		if lineEnd < 0 {
			break
		}
		position = next
	}
	return frontmatter{}, nil, fmt.Errorf("frontmatter closing delimiter not found")
}

func normalizeDescription(description string) string {
	description = strings.ToValidUTF8(description, "�")
	description = strings.Join(strings.Fields(description), " ")
	if len(description) <= maxCatalogDescriptionBytes {
		return description
	}
	limit := maxCatalogDescriptionBytes - len("...")
	for limit > 0 && (description[limit]&0xc0) == 0x80 {
		limit--
	}
	return description[:limit] + "..."
}

// bound truncates an injected document to the maximum skill size.
func bound(content []byte) string {
	if len(content) <= maxSkillBytes {
		return string(content)
	}
	content = content[:maxSkillBytes]
	for len(content) > 0 && !utf8.Valid(content) {
		content = content[:len(content)-1]
	}
	return string(content)
}
