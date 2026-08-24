package tools

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"sort"
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
	Digest      string
	Diagnostics []Diagnostic
}

// ErrUnknownSkill is returned when a name was not present in the last scan.
var ErrUnknownSkill = errors.New("unknown skill")

// Registry stores scanned metadata and paths, but never full skill bodies.
type Registry struct {
	mu          sync.RWMutex
	source      fs.FS
	catalog     []Skill
	paths       map[string]string
	digest      string
	diagnostics []Diagnostic
}

// NewRegistry binds a registry to source. Skills are discovered by Scan.
func NewRegistry(source fs.FS) *Registry {
	return &Registry{source: source, paths: make(map[string]string)}
}

// HasSkills reports whether the last scan found at least one valid skill.
func (registry *Registry) HasSkills() bool {
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return len(registry.catalog) != 0
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
	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	result.Catalog = catalog
	result.Digest = catalogDigest(catalog)
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
	registry.digest = result.Digest
	registry.diagnostics = append([]Diagnostic(nil), result.Diagnostics...)
}

func cloneScanResult(result ScanResult) ScanResult {
	result.Catalog = append([]Skill(nil), result.Catalog...)
	result.Diagnostics = append([]Diagnostic(nil), result.Diagnostics...)
	return result
}

// Catalog returns a defensive copy of sorted scanned metadata.
func (registry *Registry) Catalog() []Skill {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return append([]Skill(nil), registry.catalog...)
}

// Diagnostics returns a defensive copy of the last scan diagnostics.
func (registry *Registry) Diagnostics() []Diagnostic {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return append([]Diagnostic(nil), registry.diagnostics...)
}

// Digest returns the stable digest of the last scanned catalog.
func (registry *Registry) Digest() string {
	if registry == nil {
		return ""
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.digest
}

// Names returns sorted names accepted by Load.
func (registry *Registry) Names() []string {
	catalog := registry.Catalog()
	names := make([]string, 0, len(catalog))
	for _, skill := range catalog {
		names = append(names, skill.Name)
	}
	return names
}

// RenderCatalog renders compact metadata without any skill body.
func (registry *Registry) RenderCatalog(replacement bool) string {
	if registry == nil {
		return bound([]byte("<pentgo-skill-catalog digest=\"\">\n</pentgo-skill-catalog>"))
	}
	registry.mu.RLock()
	catalog := append([]Skill(nil), registry.catalog...)
	digest := registry.digest
	registry.mu.RUnlock()

	var builder strings.Builder
	fmt.Fprintf(&builder, "<pentgo-skill-catalog digest=\"%s\">\n", digest)
	if replacement {
		builder.WriteString("This catalog completely replaces every earlier PentGo skill catalog in this session.\n")
	}
	if len(catalog) == 0 {
		if replacement {
			builder.WriteString("No PentGo skills are currently available. Do not use names from earlier PentGo skill catalogs.\n")
		}
	} else {
		builder.WriteString("Available PentGo skills:\n")
		for _, skill := range catalog {
			fmt.Fprintf(&builder, "- `%s`：%s\n", skill.Name, skill.Description)
		}
		builder.WriteString("When the task clearly matches a listed skill, call load_skill with its exact name before specialized work. Do not guess skill names; if no entry matches, continue normally.\n")
	}
	builder.WriteString("</pentgo-skill-catalog>")
	return bound([]byte(builder.String()))
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

func catalogDigest(catalog []Skill) string {
	if len(catalog) == 0 {
		return ""
	}
	hash := sha256.New()
	for _, skill := range catalog {
		fmt.Fprintf(hash, "%s\n%s\n", skill.Name, skill.Description)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
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
