package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"pentgo/internal/adapters/builtins"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

// SkillLoader returns complete documentation for exactly one discovered skill.
type SkillLoader func(string) (string, error)

// runtimeToolProvider builds tools visible to one session turn. The optional
// skill loader comes from process-start discovery; its catalog remains in the
// persisted session transcript rather than this turn's tool configuration.
type runtimeToolProvider struct {
	runtime         *ProjectRuntime
	session         *domain.Session
	projectTools    []agent.Tool
	loadSkill       SkillLoader
	skillsAvailable bool
}

func newRuntimeToolProvider(runtime *ProjectRuntime, session *domain.Session, projectTools []agent.Tool, loadSkill SkillLoader, skillsAvailable bool) *runtimeToolProvider {
	return &runtimeToolProvider{runtime: runtime, session: session, projectTools: append([]agent.Tool(nil), projectTools...), loadSkill: loadSkill, skillsAvailable: skillsAvailable}
}

// Tools combines session built-ins, host-owned project-fact tools, and
// project LocalRegistry / external MCP tools without allowing name shadowing.
func (provider *runtimeToolProvider) Tools(context.Context) ([]agent.Tool, error) {
	if provider == nil || provider.runtime == nil || provider.session == nil {
		return nil, fmt.Errorf("runtime tool provider is incomplete")
	}
	facts := provider.runtime.ProjectFacts()
	if facts == nil {
		return nil, fmt.Errorf("project fact store is unavailable")
	}
	tools := builtins.NewTools(provider.runtime.Workspace())
	tools = append(tools, newProjectFactTools(facts)...)
	if provider.loadSkill != nil && provider.skillsAvailable {
		tools = append(tools, &loadSkillTool{load: provider.loadSkill})
	}
	seen := make(map[string]bool, len(tools)+len(provider.projectTools))
	for _, tool := range tools {
		seen[tool.Name()] = true
	}
	for _, projectTool := range provider.projectTools {
		if projectTool == nil {
			return nil, fmt.Errorf("project tool is nil")
		}
		if builtins.IsName(projectTool.Name()) || seen[projectTool.Name()] {
			return nil, fmt.Errorf("tool name collision: %s", projectTool.Name())
		}
		seen[projectTool.Name()] = true
		tools = append(tools, projectTool)
	}
	return tools, nil
}

// upsertProjectFactTool creates or replaces one project fact. It only decodes
// the JSON boundary and delegates business rules to the typed ledger.
type upsertProjectFactTool struct {
	facts *ProjectFactLedger
}

func (tool *upsertProjectFactTool) Name() string { return "upsert_project_fact" }

func (tool *upsertProjectFactTool) Description() string {
	return "创建或覆盖一个项目事实；可选用一个已存在 Evidence 序号作为来源引用。"
}

func (tool *upsertProjectFactTool) InputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"key", "value"},
		"properties": map[string]any{
			"key":          map[string]any{"type": "string"},
			"value":        map[string]any{"type": "string"},
			"evidence_ref": map[string]any{"type": "integer"},
		},
	}
}

func (tool *upsertProjectFactTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if tool == nil || tool.facts == nil {
		return "fact rejected: project fact store is unavailable", fmt.Errorf("project fact store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	value, err := factValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	evidenceRef, err := optionalEvidenceRef(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	if err := tool.facts.Upsert(ctx, ProjectFactUpsert{Key: key, Value: value, EvidenceRef: evidenceRef}); err != nil {
		return rejectedFactToolResult(err)
	}
	return "project fact upserted: " + key, nil
}

// getProjectFactTool reads one complete project fact by key.
type getProjectFactTool struct {
	facts *ProjectFactLedger
}

func (tool *getProjectFactTool) Name() string { return "get_project_fact" }

func (tool *getProjectFactTool) Description() string {
	return "按 key 读取项目事实的完整 value 与可选 Evidence 引用；找不到时返回 project fact not found。"
}

func (tool *getProjectFactTool) InputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"key"},
		"properties": map[string]any{
			"key": map[string]any{"type": "string"},
		},
	}
}

func (tool *getProjectFactTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if tool == nil || tool.facts == nil {
		return "fact rejected: project fact store is unavailable", fmt.Errorf("project fact store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	fact, found, err := tool.facts.Get(ctx, key)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	if !found {
		return "project fact not found", nil
	}
	return renderFullFact(fact), nil
}

// listProjectFactsTool renders the fixed project-fact index without filters.
type listProjectFactsTool struct {
	facts *ProjectFactLedger
}

func (tool *listProjectFactsTool) Name() string { return "list_project_facts" }

func (tool *listProjectFactsTool) Description() string {
	return "按 key 排序列出全部项目事实的固定上限摘要。"
}

func (tool *listProjectFactsTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (tool *listProjectFactsTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if tool == nil || tool.facts == nil {
		return "fact rejected: project fact store is unavailable", fmt.Errorf("project fact store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	facts, err := tool.facts.List(ctx)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	return RenderProjectFactIndex(facts), nil
}

func newProjectFactTools(facts *ProjectFactLedger) []agent.Tool {
	return []agent.Tool{
		&upsertProjectFactTool{facts: facts},
		&getProjectFactTool{facts: facts},
		&listProjectFactsTool{facts: facts},
	}
}

func factKeyValue(arguments map[string]any) (string, error) {
	value, exists := arguments["key"]
	if !exists || value == nil {
		return "", fmt.Errorf("fact key is required")
	}
	key, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("fact key must be a string")
	}
	if err := domain.ValidateProjectFactKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func factValue(arguments map[string]any) (string, error) {
	value, exists := arguments["value"]
	if !exists || value == nil {
		return "", fmt.Errorf("value is required")
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("value must be a string")
	}
	return text, nil
}

func optionalEvidenceRef(arguments map[string]any) (*int, error) {
	value, exists := arguments["evidence_ref"]
	if !exists || value == nil {
		return nil, nil
	}
	ref, ok := integerValue(value)
	if !ok {
		return nil, fmt.Errorf("evidence_ref must be an integer or null")
	}
	return &ref, nil
}

func integerValue(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		if number != float64(int(number)) {
			return 0, false
		}
		return int(number), true
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func rejectedFactToolResult(err error) (string, error) {
	message := publicFactToolError(err)
	return "fact rejected: " + message, fmt.Errorf("%s", message)
}

// publicFactToolError keeps model-visible/Evidence failure text actionable
// without reflecting database paths, SQL fragments, or driver diagnostics.
func publicFactToolError(err error) string {
	if err == nil {
		return "project fact operation failed"
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	if strings.Contains(lower, "sql") || strings.Contains(lower, "sqlite") || strings.Contains(lower, "database") || strings.Contains(lower, "driver") {
		return "project fact storage operation failed"
	}
	if utf8.RuneCountInString(message) > 512 {
		return string([]rune(message)[:512]) + "…"
	}
	return message
}

func renderFullFact(fact domain.ProjectFact) string {
	if fact.EvidenceRef != nil {
		return fmt.Sprintf("key: %s\nvalue: %s\nevidence_ref: %d", fact.Key, fact.Value, *fact.EvidenceRef)
	}
	return fmt.Sprintf("key: %s\nvalue: %s", fact.Key, fact.Value)
}

// loadSkillTool reads the selected discovered skill text.
type loadSkillTool struct{ load SkillLoader }

func (*loadSkillTool) Name() string { return "load_skill" }
func (*loadSkillTool) Description() string {
	return "按准确注册名称加载一个专用 PentGo 技能正文。"
}
func (*loadSkillTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}}
}
func (tool *loadSkillTool) Invoke(_ context.Context, arguments map[string]any) (string, error) {
	name := strings.TrimSpace(stringValue(arguments, "name"))
	if name == "" {
		return "skill rejected: name is required", nil
	}
	content, err := tool.load(name)
	if err != nil {
		return "skill rejected: " + err.Error(), nil
	}
	return "=== 技能正文开始 ===\n技能：" + name + "\n" + content + "\n=== 技能正文结束===", nil
}

func stringValue(arguments map[string]any, key string) string {
	if arguments == nil {
		return ""
	}
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}
