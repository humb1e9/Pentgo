package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"pentgo/internal/adapters/llm"
	mcpadapter "pentgo/internal/adapters/mcp"
	skillsadapter "pentgo/internal/adapters/skillfs"
	"pentgo/internal/adapters/storage"
	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/domain"

	"github.com/cloudwego/eino/components/model"
)

// 打开目录运行时时使用的工作区和默认会话配置。
const (
	workspaceDirectory   = ".pentgo"
	defaultSessionIntent = "交互会话"
)

// ErrProjectNotFound 表示工作区和旧版根目录中均不存在 PentGo 项目存储。
var ErrProjectNotFound = errors.New("current directory is not a PentGo project")

// Dependencies 包含 Coordinator 使用的可替换进程级依赖。
// 测试中可注入时钟、模型工厂或技能文件系统。
type Dependencies struct {
	Clock    func() time.Time
	NewModel func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error)
	SkillsFS fs.FS
}

// Coordinator 负责打开和关闭一个目录的 ProjectRuntime。它是 CLI 使用的门面，
// 并串行化项目生命周期状态迁移。
type Coordinator struct {
	mu        sync.RWMutex
	lifecycle sync.Mutex
	cfg       config.Config
	root      string
	deps      Dependencies
	store     *storage.ProjectStore
	runtime   *ProjectRuntime
	service   *TurnService
	skills    *skillsadapter.Registry
}

// New 创建以 outputRoot 为根目录的 Coordinator，并为未提供的依赖补充生产默认值。
func New(cfg config.Config, outputRoot string, deps Dependencies) *Coordinator {
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewModel == nil {
		deps.NewModel = llm.NewModel
	}
	return &Coordinator{cfg: cfg, root: strings.TrimSpace(outputRoot), deps: deps, skills: skillsadapter.NewRegistry(deps.SkillsFS)}
}

// LoadSkills 扫描显式指定的技能文件系统，并将摘要提供给当前项目后续配置的 turn。
func (coordinator *Coordinator) LoadSkills() (string, error) {
	if coordinator == nil || coordinator.skills == nil {
		return "", fmt.Errorf("skill registry is unavailable")
	}
	summary, err := coordinator.skills.Scan()
	if err != nil {
		return "", err
	}
	coordinator.mu.RLock()
	service := coordinator.service
	coordinator.mu.RUnlock()
	if service != nil {
		service.SetSkillLoader(coordinator.skills.Load, summary)
	}
	return summary, nil
}

// CreateProject 在 Coordinator 根目录初始化并打开项目。
func (coordinator *Coordinator) CreateProject(ctx context.Context, name string) (*domain.Project, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("coordinator is nil")
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	if coordinator.HasProject() {
		return nil, fmt.Errorf("a project is already open")
	}
	store, err := storage.CreateProjectStore(coordinator.root, name, coordinator.now())
	if err != nil {
		return nil, err
	}
	if err := coordinator.openStore(ctx, store); err != nil {
		return nil, err
	}
	return coordinator.CurrentProjectValue(), nil
}

// OpenCurrentProject 从工作区或根目录打开已有项目。
func (coordinator *Coordinator) OpenCurrentProject(ctx context.Context) (*domain.Project, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("coordinator is nil")
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	return coordinator.openCurrentProjectLocked(ctx)
}

// OpenOrCreateWorkspace 复用已有项目，或在当前工作目录创建 .pentgo/。
// 返回的 bool 表示是否新建了存储。
func (coordinator *Coordinator) OpenOrCreateWorkspace(ctx context.Context) (*domain.Project, bool, error) {
	if coordinator == nil {
		return nil, false, fmt.Errorf("coordinator is nil")
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	project, err := coordinator.openCurrentProjectLocked(ctx)
	if err == nil {
		return project, false, nil
	}
	if !errors.Is(err, ErrProjectNotFound) {
		return nil, false, err
	}
	store, err := storage.CreateProjectStoreAt(coordinator.workspaceRoot(), filepath.Base(filepath.Clean(coordinator.root)), coordinator.now())
	if err != nil {
		return nil, false, err
	}
	if err := coordinator.openStore(ctx, store); err != nil {
		return nil, false, err
	}
	return coordinator.CurrentProjectValue(), true, nil
}

// openCurrentProjectLocked 保留对根目录旧项目布局的支持，同时优先使用当前 .pentgo 工作区布局。
func (coordinator *Coordinator) openCurrentProjectLocked(ctx context.Context) (*domain.Project, error) {
	if coordinator.HasProject() {
		return coordinator.CurrentProjectValue(), nil
	}
	for _, root := range []string{coordinator.workspaceRoot(), coordinator.root} {
		store, err := storage.OpenProjectStore(root)
		if errors.Is(err, storage.ErrNotProject) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := coordinator.openStore(ctx, store); err != nil {
			return nil, err
		}
		return coordinator.CurrentProjectValue(), nil
	}
	return nil, ErrProjectNotFound
}

// workspaceRoot 返回启动根目录下项目私有的工作区目录。
func (coordinator *Coordinator) workspaceRoot() string {
	return filepath.Join(coordinator.root, workspaceDirectory)
}

// openStore 装配证据脱敏、MCP 连接、模型构造、可选技能和恢复的会话 worker。
// 延迟清理确保部分构建失败的运行时不会遗留打开的存储或客户端进程。
func (coordinator *Coordinator) openStore(ctx context.Context, store *storage.ProjectStore) (openErr error) {
	defer func() {
		if openErr != nil {
			_ = store.Close()
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	secrets := mcpadapter.ConfigSecrets(coordinator.cfg.Agent.MCP)
	projectRuntime, err := OpenProjectRuntimeWithSecrets(ctx, store, nil, secrets...)
	if err != nil {
		return err
	}
	if len(coordinator.cfg.Agent.MCP) != 0 {
		mcpClients, connectErr := mcpadapter.ConnectAll(ctx, coordinator.cfg.Agent.MCP, projectRuntime.Evidence(), coordinator.cfg.Agent.MaxOutputBytes, store.Root(), store.TmpDir())
		if connectErr != nil {
			_ = projectRuntime.Close()
			return connectErr
		}
		if err := projectRuntime.SetToolProvider(mcpClients); err != nil {
			_ = mcpClients.Close()
			_ = projectRuntime.Close()
			return err
		}
	}
	service := NewTurnService(nil, store, nil)
	service.SetClock(coordinator.deps.Clock)
	service.SetEngineFactory(func(runContext context.Context, _ *domain.Session, runtime *ProjectRuntime) (agent.ModelEngine, error) {
		chatModel, err := coordinator.deps.NewModel(runContext, coordinator.cfg.Agent)
		if err != nil {
			return nil, err
		}
		engine, err := llm.NewEngine(runContext, chatModel, nil, runtime.Workspace(), func(_ context.Context, name string, arguments map[string]any, success bool, output string) (string, error) {
			if !success {
				output = "工具调用失败：" + output
			}
			record, recordErr := runtime.Evidence().RecordResult(context.Background(), name, arguments, success, output)
			if recordErr != nil {
				return "", recordErr
			}
			return record.Output, nil
		})
		if err != nil {
			return nil, err
		}
		engine.SetMaxIterations(coordinator.cfg.Agent.MaxTurns)
		return engine, nil
	})
	if coordinator.skills != nil && coordinator.skills.Loaded() {
		summary, summaryErr := coordinator.skills.Summary()
		if summaryErr != nil {
			_ = projectRuntime.Close()
			return summaryErr
		}
		service.SetSkillLoader(coordinator.skills.Load, summary)
	}
	if err := projectRuntime.SetTurnHandler(func(runContext context.Context, session *domain.Session, message string) error {
		return service.RunTurn(runContext, projectRuntime, session, message)
	}); err != nil {
		_ = projectRuntime.Close()
		return err
	}
	coordinator.mu.Lock()
	coordinator.store = store
	coordinator.runtime = projectRuntime
	coordinator.service = service
	coordinator.mu.Unlock()
	if err := projectRuntime.RestoreSessions(); err != nil {
		_ = coordinator.closeProjectLocked()
		return err
	}
	return nil
}

// HasProject 表示当前 Coordinator 是否持有已打开的运行时。
func (coordinator *Coordinator) HasProject() bool {
	if coordinator == nil {
		return false
	}
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	return coordinator.runtime != nil
}

// CurrentProject 在运行时打开时返回安全的项目快照。
func (coordinator *Coordinator) CurrentProject() (*domain.Project, bool) {
	if coordinator == nil {
		return nil, false
	}
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil, false
	}
	return runtime.Project(), true
}

// CurrentProjectValue 是省略 bool 返回值的 CurrentProject 便捷形式。
func (coordinator *Coordinator) CurrentProjectValue() *domain.Project {
	project, _ := coordinator.CurrentProject()
	return project
}

// NewSession 打开新的运行时会话；调用方未显式提供目标时，从 intent 中提取目标。
func (coordinator *Coordinator) NewSession(intent string, targets ...string) (*domain.Session, error) {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("no project is open")
	}
	if len(targets) == 0 {
		targets = extractTargets(intent)
	}
	return runtime.NewSession(intent, targets...)
}

// ResumeSession 校验指定会话已附加到运行时。
func (coordinator *Coordinator) ResumeSession(id string) (*domain.Session, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("coordinator is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("session id is empty")
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("no project is open")
	}
	session := runtime.Snapshot(id)
	if session == nil {
		return nil, fmt.Errorf("session %q does not exist", id)
	}
	return session, nil
}

// DeleteSession 删除指定会话及其 transcript，其他会话保持运行。
func (coordinator *Coordinator) DeleteSession(id string) error {
	if coordinator == nil {
		return fmt.Errorf("coordinator is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("session id is empty")
	}
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("no project is open")
	}
	return runtime.DeleteSession(id)
}

// Submit 将消息转发给当前运行时中的会话 worker。
func (coordinator *Coordinator) Submit(ctx context.Context, sessionID, message string) <-chan error {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		done := make(chan error, 1)
		done <- fmt.Errorf("no project is open")
		return done
	}
	return runtime.Submit(ctx, sessionID, message)
}

// Sessions 返回按创建时间和 ID 确定性排序的会话快照。
func (coordinator *Coordinator) Sessions() []*domain.Session {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	sessions := runtime.Sessions()
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].StartedAt.Equal(sessions[j].StartedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	return sessions
}

// RenameSession 将持久化重命名操作转发给指定会话 worker。
func (coordinator *Coordinator) RenameSession(sessionID, name string) error {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("no project is open")
	}
	return runtime.RenameSession(sessionID, name)
}

// Events 返回会话的非持久化运行时进度事件。
func (coordinator *Coordinator) Events(sessionID string) <-chan Event {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.Events(sessionID)
}

// Messages 返回有序的持久化 transcript，供 UI 渲染或模型回放。
func (coordinator *Coordinator) Messages(sessionID string) []agent.Message {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	transcript := runtime.Transcript(sessionID)
	if transcript == nil {
		return nil
	}
	return transcript.Messages()
}

// Blackboard 将当前项目事实渲染为 CLI 文本。
func (coordinator *Coordinator) Blackboard() string {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return "当前没有打开的项目。"
	}
	return blackboardText(runtime.Blackboard())
}

// CloseProject 释放当前运行时及其持有的全部资源。
func (coordinator *Coordinator) CloseProject() error {
	if coordinator == nil {
		return nil
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	return coordinator.closeProjectLocked()
}

// closeProjectLocked 在关闭前先解除字段引用，确保后续调用方不会看到正在关闭的资源。
func (coordinator *Coordinator) closeProjectLocked() error {
	coordinator.mu.Lock()
	runtime := coordinator.runtime
	store := coordinator.store
	coordinator.runtime = nil
	coordinator.store = nil
	coordinator.service = nil
	coordinator.mu.Unlock()
	var closeErr error
	if runtime != nil {
		closeErr = runtime.Close()
	} else if store != nil {
		closeErr = store.Close()
	}
	return closeErr
}

// now 返回 Coordinator 时钟提供的 UTC 时间。
func (coordinator *Coordinator) now() time.Time {
	if coordinator == nil || coordinator.deps.Clock == nil {
		return time.Now().UTC()
	}
	return coordinator.deps.Clock().UTC()
}

// targetPattern 用于识别用户 intent 中的 URL 和类主机字符串。
var targetPattern = regexp.MustCompile(`(?i)https?://[^\s<>"',，。；、]+|(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}(?::[0-9]{1,5})?(?:/[^\s<>"',，。；、]*)?`)

// extractTargets 将识别出的目标规范化后保存到会话，并删除仅主机或协议大小写、
// URL 片段不同的重复项。
func extractTargets(intent string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, raw := range targetPattern.FindAllString(intent, -1) {
		candidate := raw
		if !strings.Contains(candidate, "://") {
			candidate = "https://" + candidate
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Fragment = ""
		canonical := parsed.String()
		if !seen[canonical] {
			seen[canonical] = true
			result = append(result, canonical)
		}
	}
	return result
}
