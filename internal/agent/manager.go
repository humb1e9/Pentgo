package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	contextpolicy "pentgo/internal/context"
	llm "pentgo/internal/model"
	projectmodel "pentgo/internal/project"
	sessionstate "pentgo/internal/session"
	"pentgo/internal/storage"
	"pentgo/internal/tools"

	einomodel "github.com/cloudwego/eino/components/model"
)

// ErrProjectNotFound 表示当前目录中不存在 .pentgo 项目存储。
var ErrProjectNotFound = errors.New("current directory is not a PentGo project")

// Dependencies 包含 Manager 使用的可替换进程级依赖。
// 测试中可注入时钟、模型工厂或技能文件系统。
type Dependencies struct {
	Clock              func() time.Time
	NewModel           func(context.Context, llm.Config) (einomodel.ToolCallingChatModel, error)
	SkillsFS           fs.FS
	DiscoverLocalTools func(tools.LocalTools, int) tools.Provider
}

// Manager 负责打开和关闭一个目录的 ProjectRuntime。它是 CLI 使用的门面，
// 并串行化项目生命周期状态迁移。
type Manager struct {
	mu               sync.RWMutex
	lifecycle        sync.Mutex
	cfg              Config
	root             string
	deps             Dependencies
	store            *storage.ProjectStore
	runtime          *ProjectRuntime
	skills           *tools.Registry
	localTools       tools.Provider
	skillDiagnostics []tools.Diagnostic
}

// New 创建以 outputRoot 为根目录的 Manager，并为未提供的依赖补充生产默认值。
func NewManager(cfg Config, outputRoot string, deps Dependencies) *Manager {
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewModel == nil {
		deps.NewModel = func(context.Context, llm.Config) (einomodel.ToolCallingChatModel, error) {
			return nil, fmt.Errorf("model factory is not configured")
		}
	}
	if deps.DiscoverLocalTools == nil {
		deps.DiscoverLocalTools = func(configurations tools.LocalTools, maximumOutputBytes int) tools.Provider {
			return tools.NewLocalRegistry(configurations, maximumOutputBytes)
		}
	}
	registry := tools.NewRegistry(deps.SkillsFS)
	result := registry.Scan()
	return &Manager{
		cfg:              cfg,
		root:             strings.TrimSpace(outputRoot),
		deps:             deps,
		skills:           registry,
		localTools:       deps.DiscoverLocalTools(cfg.Tools.Local, cfg.Tools.MaxOutputBytes),
		skillDiagnostics: append([]tools.Diagnostic(nil), result.Diagnostics...),
	}
}

// SkillDiagnostics returns unacknowledged startup skill scan diagnostics.
func (coordinator *Manager) SkillDiagnostics() []tools.Diagnostic {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	return append([]tools.Diagnostic(nil), coordinator.skillDiagnostics...)
}

// claimSkillDiagnostics exposes each unchanged scan result only once per project.
func (coordinator *Manager) claimSkillDiagnostics(store *storage.ProjectStore) error {
	if coordinator == nil || store == nil {
		return nil
	}
	coordinator.mu.RLock()
	diagnostics := append([]tools.Diagnostic(nil), coordinator.skillDiagnostics...)
	coordinator.mu.RUnlock()
	if len(diagnostics) == 0 {
		return nil
	}
	hash := sha256.New()
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(hash, "%s\x00%s\x00", diagnostic.Path, diagnostic.Reason)
	}
	claimed, err := store.ClaimNotice(fmt.Sprintf("skill-diagnostics:%x", hash.Sum(nil)))
	if err != nil {
		return err
	}
	if claimed {
		return nil
	}
	coordinator.mu.Lock()
	coordinator.skillDiagnostics = nil
	coordinator.mu.Unlock()
	return nil
}

// OpenCurrentProject 从当前目录的 .pentgo 工作区打开已有项目。
func (coordinator *Manager) OpenCurrentProject(ctx context.Context) (*projectmodel.Project, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("coordinator is nil")
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	return coordinator.openCurrentProjectLocked(ctx)
}

// OpenOrCreateWorkspace 复用已有项目，或在当前工作目录创建 .pentgo/。
// 返回的 bool 表示是否新建了存储。
func (coordinator *Manager) OpenOrCreateWorkspace(ctx context.Context) (*projectmodel.Project, bool, error) {
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
	store, err := storage.CreateProjectStore(coordinator.root, filepath.Base(filepath.Clean(coordinator.root)), coordinator.now())
	if err != nil {
		return nil, false, err
	}
	if err := coordinator.openStore(ctx, store); err != nil {
		return nil, false, err
	}
	project, _ = coordinator.CurrentProject()
	return project, true, nil
}

// openCurrentProjectLocked opens only the current directory's .pentgo store.
func (coordinator *Manager) openCurrentProjectLocked(ctx context.Context) (*projectmodel.Project, error) {
	if project, ok := coordinator.CurrentProject(); ok {
		return project, nil
	}
	store, err := storage.OpenProjectStore(coordinator.workspaceRoot())
	if errors.Is(err, storage.ErrNotProject) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := coordinator.openStore(ctx, store); err != nil {
		return nil, err
	}
	project, _ := coordinator.CurrentProject()
	return project, nil
}

// workspaceRoot 返回启动根目录下项目私有的工作区目录。
func (coordinator *Manager) workspaceRoot() string {
	return filepath.Join(coordinator.root, storage.WorkspaceDirectory)
}

// openStore 装配证据脱敏、MCP 连接、模型构造、可选技能和恢复的会话 worker。
// 延迟清理确保部分构建失败的运行时不会遗留打开的存储或客户端进程。
func (coordinator *Manager) openStore(ctx context.Context, store *storage.ProjectStore) (openErr error) {
	defer func() {
		if openErr != nil {
			_ = store.Close()
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	secrets := tools.ConfigSecrets(coordinator.cfg.Tools.MCP)
	projectRuntime, err := OpenProjectRuntime(ctx, store, nil, coordinator.cfg.Tools.MaxOutputBytes, secrets...)
	if err != nil {
		return err
	}
	if err := coordinator.claimSkillDiagnostics(store); err != nil {
		_ = projectRuntime.Close()
		return err
	}
	var externalTools tools.Provider
	if len(coordinator.cfg.Tools.MCP) != 0 {
		mcpClients, connectErr := tools.ConnectAll(ctx, coordinator.cfg.Tools.MCP, projectRuntime.Evidence(), coordinator.cfg.Tools.MaxOutputBytes, store.Root(), store.TmpDir())
		if connectErr != nil {
			_ = projectRuntime.Close()
			return connectErr
		}
		externalTools = mcpClients
	}
	projectTools := combineToolProviders(coordinator.localTools, externalTools)
	if err := validateProjectTools(ctx, projectTools); err != nil {
		_ = projectTools.Close()
		_ = projectRuntime.Close()
		return err
	}
	if err := projectRuntime.SetToolProvider(projectTools); err != nil {
		_ = projectTools.Close()
		_ = projectRuntime.Close()
		return err
	}
	summaryWriter := contextpolicy.NewModelSummaryWriter(coordinator.deps.NewModel, coordinator.cfg.Model)
	service := NewTurnService(TurnServiceConfig{
		NewModel: func(runContext context.Context) (einomodel.ToolCallingChatModel, error) {
			return coordinator.deps.NewModel(runContext, coordinator.cfg.Model)
		},
		SkillContext: func(request string) string {
			return matchedSkillContext(coordinator.skills, request)
		},
		Clock:       coordinator.deps.Clock,
		MaxRequests: coordinator.cfg.Project.MaxTurns,
		Context:     coordinator.cfg.Project.Context,
		Summarizer:  summaryWriter,
	})
	projectRuntime.SetPauseHandler(service.PauseSession)
	if err := projectRuntime.SetTurnHandler(func(runContext context.Context, session *sessionstate.Session, message string) error {
		return service.RunTurn(runContext, projectRuntime, session, message)
	}); err != nil {
		_ = projectRuntime.Close()
		return err
	}
	coordinator.mu.Lock()
	coordinator.store = store
	coordinator.runtime = projectRuntime
	coordinator.mu.Unlock()
	if err := projectRuntime.RestoreSessions(); err != nil {
		_ = coordinator.closeProjectLocked()
		return err
	}
	return nil
}

// CurrentProject 在运行时打开时返回安全的项目快照。
func (coordinator *Manager) CurrentProject() (*projectmodel.Project, bool) {
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

// NewSession 打开新的运行时会话。
func (coordinator *Manager) NewSession(intent string) (*sessionstate.Session, error) {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("no project is open")
	}
	session, err := runtime.NewSession(intent)
	if err != nil {
		return nil, err
	}
	return runtime.Snapshot(session.ID), nil
}

// ResumeSession 校验指定会话已附加到运行时。
func (coordinator *Manager) ResumeSession(id string) (*sessionstate.Session, error) {
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
	return runtime.Snapshot(id), nil
}

// DeleteSession 删除指定会话及其 conversation，其他会话保持运行。
func (coordinator *Manager) DeleteSession(id string) error {
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

// PauseSession cancels the active turn without closing its session.
func (coordinator *Manager) PauseSession(sessionID string) error {
	if coordinator == nil {
		return fmt.Errorf("coordinator is nil")
	}
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("no project is open")
	}
	return runtime.PauseSession(sessionID)
}

// ResumeTurn continues a checkpointed interrupted turn in the current project.
func (coordinator *Manager) ResumeTurn(ctx context.Context, sessionID string) <-chan error {
	done := make(chan error, 1)
	if coordinator == nil {
		done <- fmt.Errorf("coordinator is nil")
		return done
	}
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		done <- fmt.Errorf("no project is open")
		return done
	}
	return runtime.ResumeTurn(ctx, sessionID)
}

// Submit 将消息转发给当前运行时中的会话 worker。
func (coordinator *Manager) Submit(ctx context.Context, sessionID, message string) <-chan error {
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
func (coordinator *Manager) Sessions() []*sessionstate.Session {
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

// Events 返回会话的非持久化运行时进度事件。
func (coordinator *Manager) Events(sessionID string) <-chan sessionstate.Event {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.Events(sessionID)
}

// Messages 返回有序的持久化 conversation，供 UI 渲染或模型回放。
func (coordinator *Manager) Messages(sessionID string) []sessionstate.Message {
	coordinator.mu.RLock()
	runtime := coordinator.runtime
	coordinator.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	conversation := runtime.Conversation(sessionID)
	if conversation == nil {
		return nil
	}
	return conversation.Messages()
}

// CloseProject 释放当前运行时及其持有的全部资源。
func (coordinator *Manager) CloseProject() error {
	if coordinator == nil {
		return nil
	}
	coordinator.lifecycle.Lock()
	defer coordinator.lifecycle.Unlock()
	return coordinator.closeProjectLocked()
}

// closeProjectLocked 在关闭前先解除字段引用，确保后续调用方不会看到正在关闭的资源。
func (coordinator *Manager) closeProjectLocked() error {
	coordinator.mu.Lock()
	runtime := coordinator.runtime
	store := coordinator.store
	coordinator.runtime = nil
	coordinator.store = nil
	coordinator.mu.Unlock()
	var closeErr error
	if runtime != nil {
		closeErr = runtime.Close()
	} else if store != nil {
		closeErr = store.Close()
	}
	return closeErr
}

// now 返回 Manager 时钟提供的 UTC 时间。
func (coordinator *Manager) now() time.Time {
	if coordinator == nil || coordinator.deps.Clock == nil {
		return time.Now().UTC()
	}
	return coordinator.deps.Clock().UTC()
}
