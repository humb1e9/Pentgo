package agent

import "context"

// Tool 表示单轮执行时暴露给模型的可调用能力。Invoke 返回可回填给模型的文本，
// 并且必须遵循 ctx 的取消信号。
type Tool interface {
	Name() string
	Description() string
	Invoke(context.Context, map[string]any) (string, error)
}

// ToolSchemaProvider 为可选接口。了解模型输入 Schema 的适配器实现该接口，
// 以保留工具定义中的必填字段和参数类型；普通 Tool 使用通用对象 Schema 也可调用。
type ToolSchemaProvider interface {
	InputSchema() map[string]any
}

// ToolProvider 解析特定运行时上下文可用的工具。
// 实现可以发现远端工具，也可以构造会话级工具。
type ToolProvider interface {
	Tools(context.Context) ([]Tool, error)
}

// ToolCloser 让项目运行时能释放 Provider 持有的进程或网络连接，
// 而无需依赖具体适配器类型。
type ToolCloser interface {
	Close() error
}
