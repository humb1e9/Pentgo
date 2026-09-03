package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	contextpolicy "pentgo/internal/context"
	projectmodel "pentgo/internal/project"
	"pentgo/internal/tools"
)

// upsertProjectFactTool creates or replaces one project fact. It only decodes
// the JSON boundary and delegates business rules to the typed ledger.
type upsertProjectFactTool struct {
	facts *ProjectFactLedger
}

func (tool *upsertProjectFactTool) Name() string { return tools.FactUpsertName }

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
	if err := tool.facts.Upsert(ctx, projectmodel.ProjectFact{Key: key, Value: value, EvidenceRef: evidenceRef}); err != nil {
		return rejectedFactToolResult(err)
	}
	return "project fact upserted: " + key, nil
}

// getProjectFactTool reads one complete project fact by key.
type getProjectFactTool struct {
	facts *ProjectFactLedger
}

func (tool *getProjectFactTool) Name() string { return tools.FactGetName }

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

func (tool *listProjectFactsTool) Name() string { return tools.FactListName }

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
	return contextpolicy.RenderProjectFactIndex(facts), nil
}

func NewProjectFactTools(facts *ProjectFactLedger) []tools.Tool {
	return []tools.Tool{
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
	if err := projectmodel.ValidateProjectFactKey(key); err != nil {
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

func renderFullFact(fact projectmodel.ProjectFact) string {
	if fact.EvidenceRef != nil {
		return fmt.Sprintf("key: %s\nvalue: %s\nevidence_ref: %d", fact.Key, fact.Value, *fact.EvidenceRef)
	}
	return fmt.Sprintf("key: %s\nvalue: %s", fact.Key, fact.Value)
}
