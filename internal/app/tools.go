package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"pentgo/internal/adapters/builtins"
	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

// SkillLoader 返回按准确名称指定的技能文档全文。
type SkillLoader func(string) (string, error)

// runtimeToolProvider 构建单个会话 turn 可见的工具集合。它在会话内提供项目事实和
// 可选技能，并为远端工具添加项目级证据持久化装饰。
type runtimeToolProvider struct {
	runtime      *ProjectRuntime
	session      *domain.Session
	external     []agent.Tool
	loadSkill    SkillLoader
	skillSummary string
}

// newRuntimeToolProvider 复制外部工具，避免 Provider 后续变化影响正在配置的 turn。
func newRuntimeToolProvider(runtime *ProjectRuntime, session *domain.Session, external []agent.Tool, loadSkill SkillLoader, skillSummary string) *runtimeToolProvider {
	return &runtimeToolProvider{runtime: runtime, session: session, external: append([]agent.Tool(nil), external...), loadSkill: loadSkill, skillSummary: skillSummary}
}

// Tools 合并会话级内置工具和项目级远端工具，并拒绝可能遮蔽 Eino Local Backend
// 能力的同名工具。
func (provider *runtimeToolProvider) Tools(context.Context) ([]agent.Tool, error) {
	if provider == nil || provider.runtime == nil || provider.session == nil {
		return nil, fmt.Errorf("runtime tool provider is incomplete")
	}
	tools := []agent.Tool{
		&writeProjectFactTool{runtime: provider.runtime, session: provider.session},
	}
	if provider.loadSkill != nil && strings.TrimSpace(provider.skillSummary) != "" {
		tools = append(tools, &loadSkillTool{load: provider.loadSkill})
	}
	seen := make(map[string]bool, len(tools)+len(provider.external))
	for _, tool := range tools {
		seen[tool.Name()] = true
	}
	for _, external := range provider.external {
		if external == nil {
			return nil, fmt.Errorf("external tool is nil")
		}
		if builtins.IsName(external.Name()) {
			return nil, fmt.Errorf("tool name collision: %s", external.Name())
		}
		if seen[external.Name()] {
			return nil, fmt.Errorf("tool name collision: %s", external.Name())
		}
		seen[external.Name()] = true
		tools = append(tools, &evidenceTool{runtime: provider.runtime, inner: external})
	}
	return tools, nil
}

// writeProjectFactTool 向当前 turn 暴露项目 Blackboard 的更新能力。
type writeProjectFactTool struct {
	runtime *ProjectRuntime
	session *domain.Session
}

// Name 返回暴露给模型的稳定事实写入工具标识。
func (*writeProjectFactTool) Name() string { return "write_project_fact" }

// Description 向模型说明事实更新的项目级作用域。
func (*writeProjectFactTool) Description() string {
	return "写入或更新可供当前项目其他会话读取的共享事实。"
}

// InputSchema 声明必填的事实 key 和 value 属性。
func (*writeProjectFactTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"key", "value"}, "properties": map[string]any{"key": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}}}
}

// Invoke 通过运行时持久化事实后，才向模型确认写入结果。
func (tool *writeProjectFactTool) Invoke(_ context.Context, arguments map[string]any) (string, error) {
	err := tool.runtime.UpdateBlackboard(func(board *domain.Blackboard) error {
		return board.NoteFact(domain.Fact{Key: stringValue(arguments, "key"), Value: stringValue(arguments, "value"), Source: "agent", SessionID: tool.session.ID, At: time.Now().UTC()})
	})
	if err != nil {
		return "fact rejected: " + err.Error(), err
	}
	return "fact recorded", nil
}

// loadSkillTool 在模型从摘要中选定技能后读取对应正文。
type loadSkillTool struct{ load SkillLoader }

// Name 返回稳定的动态技能加载工具标识。
func (*loadSkillTool) Name() string { return "load_skill" }

// Description 提示模型必须使用已注册的准确技能名称。
func (*loadSkillTool) Description() string {
	return "按准确注册名称加载一个专用 PentGo 技能正文。"
}

// InputSchema 声明加载器接受的唯一技能名称参数。
func (*loadSkillTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}}
}

// Invoke 返回从已扫描目录中选定且经过长度限制的技能正文。
func (tool *loadSkillTool) Invoke(_ context.Context, arguments map[string]any) (string, error) {
	name := strings.TrimSpace(stringValue(arguments, "name"))
	if name == "" {
		return "skill rejected: name is required", nil
	}
	content, err := tool.load(name)
	if err != nil {
		return "skill rejected: " + err.Error(), nil
	}
	return "=== 技能正文开始 ===\n技能：" + name + "\n" + content + "\n=== 技能正文结束 ===", nil
}

// evidenceTool 在将 Provider 输出返回给模型前记录证据。
// 通过项目持有的统一装饰器维护证据顺序。
type evidenceTool struct {
	runtime *ProjectRuntime
	inner   agent.Tool
}

// Name 保留底层外部工具标识。
func (tool *evidenceTool) Name() string { return tool.inner.Name() }

// Description 保留底层外部工具描述。
func (tool *evidenceTool) Description() string { return tool.inner.Description() }

// InputSchema 保留外部工具可选的 Provider 专属 Schema。
func (tool *evidenceTool) InputSchema() map[string]any {
	if provider, ok := tool.inner.(agent.ToolSchemaProvider); ok {
		return provider.InputSchema()
	}
	return nil
}

// Invoke 执行被包装的工具，并在模型看到结果前持久化成功或失败信息。工具调用结束后，
// journal 使用后台 context 写入，避免取消请求抹除已完成的证据。
func (tool *evidenceTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	output, invokeErr := tool.inner.Invoke(ctx, arguments)
	success := invokeErr == nil
	if invokeErr != nil {
		output = "工具调用失败：" + invokeErr.Error()
	}
	// 即使底层工具返回后请求 context 被取消，仍要持久化结果；证据 journal 是持久化审计链路。
	record, recordErr := tool.runtime.Evidence().RecordResult(context.Background(), tool.inner.Name(), arguments, success, output)
	if recordErr != nil {
		return "", recordErr
	}
	return record.Output, nil
}

// stringValue 从工具参数中安全读取经 Trim 的字符串属性。
func stringValue(arguments map[string]any, key string) string {
	if arguments == nil {
		return ""
	}
	value, _ := arguments[key].(string)
	return strings.TrimSpace(value)
}

// blackboardText 将事实渲染为可注入模型系统提示词的文本。
func blackboardText(board *domain.Blackboard) string {
	if board == nil || len(board.Facts) == 0 {
		return "当前没有记录项目事实。"
	}
	lines := make([]string, 0, len(board.Facts))
	for _, fact := range board.Facts {
		line := "- " + fact.Key + "：" + fact.Value
		if fact.Source != "" {
			line += "（来源：" + fact.Source + "）"
		}
		if fact.SessionID != "" {
			line += "（会话：" + fact.SessionID + "）"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
