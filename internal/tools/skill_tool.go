package tools

import (
	"context"
	"strings"

	"pentgo/internal/core"
)

// SkillLoader returns complete Markdown for exactly one catalogued user skill.
type SkillLoader func(string) (string, error)

type skillTool struct{ load SkillLoader }

// NewSkillTool exposes exactly one discovered skill catalog to a model turn.
func NewSkillTool(load SkillLoader) core.Tool { return &skillTool{load: load} }

func (*skillTool) Name() string { return "load_skill" }
func (*skillTool) Description() string {
	return "按准确注册名称加载一个专用 PentGo 技能正文。"
}
func (*skillTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}}
}
func (tool *skillTool) Invoke(_ context.Context, arguments map[string]any) (string, error) {
	name, _ := arguments["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "skill rejected: name is required", nil
	}
	content, err := tool.load(name)
	if err != nil {
		return "skill rejected: " + err.Error(), nil
	}
	return "=== 技能正文开始 ===\n技能：" + name + "\n" + content + "\n=== 技能正文结束===", nil
}
