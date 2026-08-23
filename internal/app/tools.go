package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"pentgo/internal/adapters/builtins"
	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

const (
	maxFactBodyRunes       = 16 * 1024
	maxFactSummaryRunes    = 2048
	maxFactQueryRunes      = 256
	maxFactToolLimit       = 100
	maxFactToolEdges       = 64
	maxFactToolOutputRunes = maxFactBodyRunes + maxFactSummaryRunes + 1024
	factListOmissionRunes  = 96
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

// Tools combines session built-ins, host-owned structured fact tools, and
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
	tools = append(tools, newProjectFactTools(facts, provider.session.ID)...)
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

type projectFactToolKind string

const (
	upsertProjectFactTool    projectFactToolKind = "upsert_project_fact"
	getProjectFactTool       projectFactToolKind = "get_project_fact"
	listProjectFactsTool     projectFactToolKind = "list_project_facts"
	searchProjectFactsTool   projectFactToolKind = "search_project_facts"
	deprecateProjectFactTool projectFactToolKind = "deprecate_project_fact"
	restoreProjectFactTool   projectFactToolKind = "restore_project_fact"
)

// projectFactTool is a narrow host adapter over the project-scoped ledger. It
// returns ordinary tool results so the host Evidence executor remains the sole
// audit path for each model tool call.
type projectFactTool struct {
	kind      projectFactToolKind
	facts     *storage.ProjectFactStore
	sessionID string
}

func newProjectFactTools(facts *storage.ProjectFactStore, sessionID string) []agent.Tool {
	kinds := []projectFactToolKind{upsertProjectFactTool, getProjectFactTool, listProjectFactsTool, searchProjectFactsTool, deprecateProjectFactTool, restoreProjectFactTool}
	tools := make([]agent.Tool, 0, len(kinds))
	for _, kind := range kinds {
		tools = append(tools, &projectFactTool{kind: kind, facts: facts, sessionID: sessionID})
	}
	return tools
}

func (tool *projectFactTool) Name() string { return string(tool.kind) }

func (tool *projectFactTool) Description() string {
	switch tool.kind {
	case upsertProjectFactTool:
		return "原子地创建或更新结构化项目事实，并用提供的边集合替换该事实的出边；confirmed 必须引用成功的 Evidence。"
	case getProjectFactTool:
		return "按 key 读取一个项目事实的完整正文、置信度和 Evidence 引用。"
	case listProjectFactsTool:
		return "按确定性项目事实顺序列出摘要；默认不列出 deprecated 事实。"
	case searchProjectFactsTool:
		return "在项目事实的 key、摘要和正文中搜索，返回有界摘要；默认不搜索 deprecated 事实。"
	case deprecateProjectFactTool:
		return "将项目事实标记为 deprecated，保留可审计的正文、Evidence 与边。"
	case restoreProjectFactTool:
		return "将 deprecated 项目事实恢复为 tentative 或 confirmed；confirmed 要求保留的 Evidence 成功。"
	default:
		return ""
	}
}

func (tool *projectFactTool) InputSchema() map[string]any {
	stringSchema := map[string]any{"type": "string"}
	limitSchema := map[string]any{"type": "integer", "minimum": 1, "maximum": maxFactToolLimit}
	switch tool.kind {
	case upsertProjectFactTool:
		return map[string]any{
			"type": "object", "required": []any{"key", "category", "summary", "body", "confidence"},
			"properties": map[string]any{
				"key": stringSchema, "category": stringSchema, "summary": stringSchema, "body": stringSchema,
				"confidence": stringSchema, "pinned": map[string]any{"type": "boolean"},
				"evidence_refs": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "maxItems": maxFactToolEdges},
				"edges": map[string]any{"type": "array", "maxItems": maxFactToolEdges, "items": map[string]any{
					"type": "object", "required": []any{"target_key", "edge_type", "confidence"},
					"properties": map[string]any{"target_key": stringSchema, "edge_type": stringSchema, "confidence": stringSchema},
				}},
			},
		}
	case getProjectFactTool, deprecateProjectFactTool:
		return map[string]any{"type": "object", "required": []any{"key"}, "properties": map[string]any{"key": stringSchema}}
	case restoreProjectFactTool:
		return map[string]any{"type": "object", "required": []any{"key", "confidence"}, "properties": map[string]any{"key": stringSchema, "confidence": stringSchema}}
	case listProjectFactsTool:
		return map[string]any{"type": "object", "properties": map[string]any{"category": stringSchema, "confidence": stringSchema, "limit": limitSchema}}
	case searchProjectFactsTool:
		return map[string]any{"type": "object", "required": []any{"query"}, "properties": map[string]any{"query": stringSchema, "category": stringSchema, "confidence": stringSchema, "limit": limitSchema}}
	default:
		return map[string]any{"type": "object"}
	}
}

func (tool *projectFactTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if tool == nil || tool.facts == nil {
		return "fact rejected: project fact store is unavailable", fmt.Errorf("project fact store is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch tool.kind {
	case upsertProjectFactTool:
		return tool.upsert(ctx, arguments)
	case getProjectFactTool:
		return tool.get(arguments)
	case listProjectFactsTool:
		return tool.list(arguments)
	case searchProjectFactsTool:
		return tool.search(arguments)
	case deprecateProjectFactTool:
		return tool.deprecate(arguments)
	case restoreProjectFactTool:
		return tool.restore(arguments)
	default:
		return "fact rejected: unknown operation", fmt.Errorf("unknown project fact tool")
	}
}

func (tool *projectFactTool) upsert(ctx context.Context, arguments map[string]any) (string, error) {
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	fact := domain.ProjectFact{
		FactKey: key, Category: stringValue(arguments, "category"), Summary: stringValue(arguments, "summary"), Body: stringValue(arguments, "body"),
		Confidence: stringValue(arguments, "confidence"), SourceSessionID: tool.sessionID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if pinned, exists := arguments["pinned"]; exists {
		value, ok := pinned.(bool)
		if !ok {
			return rejectedFactToolResult(fmt.Errorf("pinned must be boolean"))
		}
		fact.Pinned = value
	}
	if utf8.RuneCountInString(fact.Summary) > maxFactSummaryRunes || utf8.RuneCountInString(fact.Body) > maxFactBodyRunes {
		return rejectedFactToolResult(fmt.Errorf("summary or body exceeds its size limit"))
	}
	references, err := intSliceValue(arguments, "evidence_refs", maxFactToolEdges)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	fact.EvidenceRefs = references
	edges, err := edgeValues(arguments, key)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	if err := tool.facts.Upsert(ctx, storage.ProjectFactWrite{Fact: fact, Edges: edges}); err != nil {
		return rejectedFactToolResult(err)
	}
	return fmt.Sprintf("project fact upserted: %s (%s, %s); edges=%d", fact.FactKey, fact.Category, fact.Confidence, len(edges)), nil
}

func (tool *projectFactTool) get(arguments map[string]any) (string, error) {
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	fact, found, err := tool.facts.Get(key)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	if !found {
		return "project fact not found", nil
	}
	return renderFullFact(fact), nil
}

func (tool *projectFactTool) list(arguments map[string]any) (string, error) {
	query, err := factQuery(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	facts, err := tool.facts.List(query)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	return renderFactList(facts), nil
}

func (tool *projectFactTool) search(arguments map[string]any) (string, error) {
	query := stringValue(arguments, "query")
	if query == "" || utf8.RuneCountInString(query) > maxFactQueryRunes {
		return rejectedFactToolResult(fmt.Errorf("query is required and must be at most 256 runes"))
	}
	filter, err := factQuery(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	facts, err := tool.facts.Search(query, filter)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	return renderFactList(facts), nil
}

func (tool *projectFactTool) deprecate(arguments map[string]any) (string, error) {
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	if err := tool.facts.Deprecate(key); err != nil {
		return rejectedFactToolResult(err)
	}
	return "project fact deprecated: " + key, nil
}

func (tool *projectFactTool) restore(arguments map[string]any) (string, error) {
	key, err := factKeyValue(arguments)
	if err != nil {
		return rejectedFactToolResult(err)
	}
	confidence := stringValue(arguments, "confidence")
	if err := tool.facts.Restore(key, confidence); err != nil {
		return rejectedFactToolResult(err)
	}
	return "project fact restored: " + key + " (" + confidence + ")", nil
}

func factKeyValue(arguments map[string]any) (string, error) {
	key := stringValue(arguments, "key")
	if key == "" || utf8.RuneCountInString(key) > domain.MaxProjectFactKeyRunes {
		return "", fmt.Errorf("key is required and must be at most %d runes", domain.MaxProjectFactKeyRunes)
	}
	return key, nil
}

func factQuery(arguments map[string]any) (storage.FactQuery, error) {
	query := storage.FactQuery{Category: stringValue(arguments, "category"), Confidence: stringValue(arguments, "confidence"), Limit: maxFactToolLimit}
	if limit, exists := arguments["limit"]; exists {
		value, ok := positiveInt(limit)
		if !ok || value > maxFactToolLimit {
			return storage.FactQuery{}, fmt.Errorf("limit must be an integer between 1 and %d", maxFactToolLimit)
		}
		query.Limit = value
	}
	return query, nil
}

func intSliceValue(arguments map[string]any, key string, maximum int) ([]int, error) {
	value, exists := arguments[key]
	if !exists || value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok || len(values) > maximum {
		return nil, fmt.Errorf("%s must be an array with at most %d integer values", key, maximum)
	}
	result := make([]int, 0, len(values))
	for _, raw := range values {
		integer, ok := positiveInt(raw)
		if !ok {
			return nil, fmt.Errorf("%s must contain positive integers", key)
		}
		result = append(result, integer)
	}
	return result, nil
}

func edgeValues(arguments map[string]any, sourceKey string) ([]domain.ProjectFactEdge, error) {
	value, exists := arguments["edges"]
	if !exists || value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok || len(values) > maxFactToolEdges {
		return nil, fmt.Errorf("edges must be an array with at most %d items", maxFactToolEdges)
	}
	edges := make([]domain.ProjectFactEdge, 0, len(values))
	for _, raw := range values {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("each edge must be an object")
		}
		edges = append(edges, domain.ProjectFactEdge{SourceFactKey: sourceKey, TargetFactKey: stringValue(object, "target_key"), EdgeType: stringValue(object, "edge_type"), Confidence: stringValue(object, "confidence")})
	}
	return edges, nil
}

func positiveInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, number > 0
	case int64:
		return int(number), number > 0 && int64(int(number)) == number
	case float64:
		return int(number), number > 0 && number == float64(int(number))
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
	return fmt.Sprintf("key: %s\ncategory: %s\nconfidence: %s\npinned: %t\nevidence_refs: %v\nsummary: %s\nbody:\n%s", fact.FactKey, fact.Category, fact.Confidence, fact.Pinned, fact.EvidenceRefs, fact.Summary, fact.Body)
}

func renderFactList(facts []domain.ProjectFact) string {
	if len(facts) == 0 {
		return "no matching project facts"
	}
	lines := make([]string, 0, len(facts))
	used := 0
	for index, fact := range facts {
		line := fmt.Sprintf("- %s [%s/%s]: %s", fact.FactKey, fact.Category, fact.Confidence, fact.Summary)
		cost := utf8.RuneCountInString(line)
		if used+cost+factListOmissionRunes > maxFactToolOutputRunes {
			omitted := fmt.Sprintf("… %d additional facts omitted; refine the query or use get_project_fact.", len(facts)-index)
			if len(lines) == 0 {
				return omitted
			}
			return strings.Join(lines, "\n") + "\n" + omitted
		}
		lines = append(lines, line)
		used += cost
	}
	return strings.Join(lines, "\n")
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
