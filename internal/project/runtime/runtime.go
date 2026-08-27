package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"pentgo/internal/core"
	"pentgo/internal/project"
	projectmodel "pentgo/internal/project"
	sessionstate "pentgo/internal/project/session"
	"pentgo/internal/project/turn"
	builtins "pentgo/internal/tools"
)

// ProjectRuntime 是项目级资源的唯一所有者。会话状态保留在各自的 sessionstate.Worker 中；
// 此类型仅保存不可变引用和已发布快照。
type ProjectRuntime struct {
	mu        sync.RWMutex
	lifecycle sync.Mutex
	commitMu  sync.Mutex
	store     *project.ProjectStore
	project   *projectmodel.Project
	facts     *turn.ProjectFactLedger
	factIndex *turn.ProjectFactIndex
	journal   *turn.EvidenceStore
	workspace *builtins.Workspace
	tools     core.ToolProvider
	turn      sessionstate.TurnFunc
	pause     func(string) bool
	sessions  map[string]*sessionRuntime
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	closeDone chan struct{}
	closeErr  error
}

// sessionRuntime 将 worker 与其后续 turn 回放所需的 conversation 配对保存。
type sessionRuntime struct {
	worker       *sessionstate.Worker
	conversation *sessionstate.ConversationStore
}

// OpenProjectRuntime 加载项目状态并打开工作区和证据 journal。调用方必须先设置
// turn handler，之后才能打开会话。
func OpenProjectRuntime(ctx context.Context, store *project.ProjectStore, tools core.ToolProvider) (*ProjectRuntime, error) {
	return openProjectRuntime(ctx, store, tools)
}

// OpenProjectRuntimeWithSecrets 额外在该运行时写入的证据中脱敏传入的敏感值。
func OpenProjectRuntimeWithSecrets(ctx context.Context, store *project.ProjectStore, tools core.ToolProvider, secrets ...string) (*ProjectRuntime, error) {
	return openProjectRuntimeWithSecrets(ctx, store, tools, secrets...)
}

// openProjectRuntime 保留供内部调用的非脱敏构造函数。
func openProjectRuntime(ctx context.Context, store *project.ProjectStore, tools core.ToolProvider) (*ProjectRuntime, error) {
	return openProjectRuntimeWithSecrets(ctx, store, tools)
}

// openProjectRuntimeWithSecrets 按 Close 的逆序创建项目资源，
// 从而在部分初始化失败时能够正确释放已打开的资源。
func openProjectRuntimeWithSecrets(ctx context.Context, store *project.ProjectStore, tools core.ToolProvider, secrets ...string) (*ProjectRuntime, error) {
	if store == nil {
		return nil, fmt.Errorf("project store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	project, err := store.LoadProject()
	if err != nil {
		return nil, err
	}
	repository, err := store.OpenProjectFactRepository()
	if err != nil {
		return nil, err
	}
	journal, err := turn.OpenEvidenceStore(store.DatabasePath(), secrets...)
	if err != nil {
		return nil, err
	}
	facts := turn.NewProjectFactLedger(repository, journal)
	factIndex := turn.NewProjectFactIndex(facts)
	workspace, err := builtins.NewWorkspace(store.WorkspaceRoot())
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	runtimeContext, cancel := context.WithCancel(ctx)
	return &ProjectRuntime{
		store:     store,
		project:   project,
		facts:     facts,
		factIndex: factIndex,
		journal:   journal,
		workspace: workspace,
		tools:     tools,
		sessions:  make(map[string]*sessionRuntime),
		ctx:       runtimeContext,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}, nil
}

// SetToolProvider 必须在任一会话打开前安装项目级外部工具，
// 确保恢复会话和新建会话看到稳定的 Provider。
func (runtime *ProjectRuntime) SetToolProvider(tools core.ToolProvider) error {
	if runtime == nil {
		return fmt.Errorf("project runtime is nil")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return fmt.Errorf("project runtime is closed")
	}
	if len(runtime.sessions) != 0 {
		return fmt.Errorf("project tools must be configured before sessions open")
	}
	runtime.tools = tools
	return nil
}

// SetPauseHandler installs an optional checkpoint-aware pause operation.
func (runtime *ProjectRuntime) SetPauseHandler(pause func(string) bool) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.pause = pause
	runtime.mu.Unlock()
}

// SetTurnHandler 安装每个 worker 执行已提交 turn 时调用的回调。
func (runtime *ProjectRuntime) SetTurnHandler(turn sessionstate.TurnFunc) error {
	if runtime == nil || turn == nil {
		return fmt.Errorf("project turn handler is invalid")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return fmt.Errorf("project runtime is closed")
	}
	runtime.turn = turn
	return nil
}

// RestoreSessions 在运行时依赖配置完成后，为每个已持久化会话重新打开 worker。
// 会话 conversation 按需回放。
func (runtime *ProjectRuntime) RestoreSessions() error {
	if runtime == nil {
		return fmt.Errorf("project runtime is nil")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.RLock()
	turn := runtime.turn
	runtime.mu.RUnlock()
	if turn == nil {
		return fmt.Errorf("project turn handler is not configured")
	}
	if runtime.closed {
		return fmt.Errorf("project runtime is closed")
	}
	project, err := runtime.store.LoadProject()
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.project = project
	runtime.mu.Unlock()
	for _, summary := range project.Sessions {
		session, loadErr := runtime.store.LoadSession(summary.ID)
		if loadErr != nil {
			return loadErr
		}
		if err := runtime.openSessionLocked(session); err != nil {
			return err
		}
	}
	return nil
}

// NewSession 启动 worker 并原子持久化其初始会话记录。
func (runtime *ProjectRuntime) NewSession(intent string, targets ...string) (*sessionstate.Session, error) {
	if runtime == nil {
		return nil, fmt.Errorf("project runtime is nil")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.RLock()
	turn := runtime.turn
	closed := runtime.closed
	runtime.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("project runtime is closed")
	}
	if turn == nil {
		return nil, fmt.Errorf("project turn handler is not configured")
	}
	session := sessionstate.NewSession("", intent, time.Now().UTC())
	session.AddTargets(targets...)
	if err := runtime.openSessionLocked(session); err != nil {
		return nil, err
	}
	runtime.commitMu.Lock()
	err := runtime.commitSessionLocked(session)
	runtime.commitMu.Unlock()
	if err != nil {
		runtime.removeSessionLocked(session.ID)
		return nil, err
	}
	return sessionstate.CloneSession(session), nil
}

// openSessionLocked 为已加载会话附加 conversation 和 worker。调用方负责串行化生命周期变更；
// 本方法在发布前再次检查运行时状态。
func (runtime *ProjectRuntime) openSessionLocked(session *sessionstate.Session) error {
	if session == nil || session.ID == "" {
		return fmt.Errorf("session is invalid")
	}
	runtime.mu.RLock()
	closed := runtime.closed
	turn := runtime.turn
	_, exists := runtime.sessions[session.ID]
	runtime.mu.RUnlock()
	if closed {
		return fmt.Errorf("project runtime is closed")
	}
	if exists {
		return fmt.Errorf("session %q is already open", session.ID)
	}
	conversation, err := runtime.store.OpenConversation(session.ID)
	if err != nil {
		return err
	}
	worker, err := sessionstate.NewWorker(runtime.ctx, session, turn)
	if err != nil {
		_ = conversation.Close()
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || runtime.sessions[session.ID] != nil {
		worker.Stop()
		go func() { <-worker.Done() }()
		_ = conversation.Close()
		return fmt.Errorf("session %q is already open", session.ID)
	}
	runtime.sessions[session.ID] = &sessionRuntime{worker: worker, conversation: conversation}
	return nil
}

// removeSessionLocked 停止新建会话持久化失败时已经创建的资源。
func (runtime *ProjectRuntime) removeSessionLocked(id string) {
	runtime.mu.Lock()
	session := runtime.sessions[id]
	delete(runtime.sessions, id)
	runtime.mu.Unlock()
	if session != nil {
		session.worker.Stop()
		<-session.worker.Done()
		_ = session.conversation.Close()
	}
}

// Submit 向指定会话排队提交消息，并返回其完成结果。
func (runtime *ProjectRuntime) Submit(ctx context.Context, sessionID, message string) <-chan error {
	if runtime == nil {
		done := make(chan error, 1)
		done <- fmt.Errorf("project runtime is nil")
		return done
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		done := make(chan error, 1)
		done <- fmt.Errorf("session %q is not available", sessionID)
		return done
	}
	return session.worker.Submit(ctx, message)
}

// PauseSession cancels the active turn while leaving its session ready for the next user message.
func (runtime *ProjectRuntime) PauseSession(sessionID string) error {
	if runtime == nil {
		return fmt.Errorf("project runtime is nil")
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("session %q is not available", sessionID)
	}
	runtime.mu.RLock()
	pause := runtime.pause
	runtime.mu.RUnlock()
	if pause != nil && pause(sessionID) {
		return nil
	}
	if !session.worker.Pause() {
		return fmt.Errorf("session %q has no running turn", sessionID)
	}
	return nil
}

// ResumeTurn continues an interrupted turn through the owning session worker.
func (runtime *ProjectRuntime) ResumeTurn(ctx context.Context, sessionID string) <-chan error {
	done := make(chan error, 1)
	if runtime == nil {
		done <- fmt.Errorf("project runtime is nil")
		return done
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		done <- fmt.Errorf("session %q is not available", sessionID)
		return done
	}
	return session.worker.Resume(ctx)
}

// RenameSession 先在 worker 中执行重命名，再提交持久化变更。提交失败时，
// 内存状态和持久化状态都会回滚至原名称。
func (runtime *ProjectRuntime) RenameSession(sessionID, name string) error {
	if runtime == nil {
		return fmt.Errorf("project runtime is nil")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("session %q is not open", sessionID)
	}
	original := session.worker.Snapshot()
	if original == nil {
		return fmt.Errorf("session %q is unavailable", sessionID)
	}
	if err := <-session.worker.Rename(name); err != nil {
		return err
	}
	renamed := session.worker.Snapshot()
	runtime.commitMu.Lock()
	defer runtime.commitMu.Unlock()
	if err := runtime.commitSessionLocked(renamed); err != nil {
		if rollbackErr := <-session.worker.Rename(original.Name); rollbackErr != nil {
			return fmt.Errorf("rename session %q: %v; restore memory: %w", sessionID, err, rollbackErr)
		}
		if restoreErr := runtime.commitSessionLocked(original); restoreErr != nil {
			return fmt.Errorf("rename session %q: %v; restore storage: %w", sessionID, err, restoreErr)
		}
		return err
	}
	return nil
}

// DeleteSession 停止指定会话的 worker，删除其持久化会话图，并更新项目摘要。
func (runtime *ProjectRuntime) DeleteSession(sessionID string) error {
	if runtime == nil {
		return fmt.Errorf("project runtime is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is empty")
	}
	runtime.lifecycle.Lock()
	defer runtime.lifecycle.Unlock()
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return fmt.Errorf("project runtime is closed")
	}
	session := runtime.sessions[sessionID]
	if session == nil {
		runtime.mu.Unlock()
		return fmt.Errorf("session %q does not exist", sessionID)
	}
	delete(runtime.sessions, sessionID)
	runtime.mu.Unlock()

	session.worker.Stop()
	<-session.worker.Done()
	var closeErr error
	if err := session.conversation.Close(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	runtime.commitMu.Lock()
	deleteErr := runtime.store.DeleteSession(sessionID)
	var loadErr error
	if deleteErr == nil {
		var project *projectmodel.Project
		project, loadErr = runtime.store.LoadProject()
		if loadErr == nil {
			runtime.mu.Lock()
			runtime.project = project
			runtime.mu.Unlock()
		}
	}
	runtime.commitMu.Unlock()
	return errors.Join(closeErr, deleteErr, loadErr)
}

// Snapshot 返回一个会话已发布状态的安全副本。
func (runtime *ProjectRuntime) Snapshot(sessionID string) *sessionstate.Session {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return nil
	}
	return session.worker.Snapshot()
}

// Sessions 返回当前附加到运行时的全部会话安全快照。
func (runtime *ProjectRuntime) Sessions() []*sessionstate.Session {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	workers := make([]*sessionstate.Worker, 0, len(runtime.sessions))
	for _, session := range runtime.sessions {
		workers = append(workers, session.worker)
	}
	runtime.mu.RUnlock()
	result := make([]*sessionstate.Session, 0, len(workers))
	for _, worker := range workers {
		if snapshot := worker.Snapshot(); snapshot != nil {
			result = append(result, snapshot)
		}
	}
	return result
}

// Events 返回指定会话的非持久化进度事件流。
func (runtime *ProjectRuntime) Events(sessionID string) <-chan sessionstate.Event {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return nil
	}
	return session.worker.Events()
}

// Emit 通过指定会话的 worker 发布进度事件。
func (runtime *ProjectRuntime) Emit(sessionID string, event turn.Event) {
	if runtime == nil {
		return
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return
	}
	if event.SessionID == "" {
		event.SessionID = sessionID
	}
	session.worker.Emit(sessionstate.Event(event))
}

// PublishSnapshot 使 worker 的最新状态对并发读取方可见。
func (runtime *ProjectRuntime) PublishSnapshot(sessionID string) {
	if runtime == nil {
		return
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return
	}
	session.worker.PublishSnapshot()
}

// Conversation 返回为下一次模型运行提供有序消息的存储对象。
func (runtime *ProjectRuntime) Conversation(sessionID string) *sessionstate.ConversationStore {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	session := runtime.sessions[sessionID]
	runtime.mu.RUnlock()
	if session == nil {
		return nil
	}
	return session.conversation
}

// Project 返回当前项目元数据及派生索引的副本。
func (runtime *ProjectRuntime) Project() *projectmodel.Project {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return projectmodel.CloneProject(runtime.project)
}

// ProjectFacts returns the project-scoped minimal fact ledger.
func (runtime *ProjectRuntime) ProjectFacts() *turn.ProjectFactLedger {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.facts
}

// ProjectFactIndex returns the read-only Fact Index renderer used to capture
// one stable snapshot at the start of every turn.
func (runtime *ProjectRuntime) ProjectFactIndex() *turn.ProjectFactIndex {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.factIndex
}

// Store 为需要访问存储的应用服务暴露项目存储对象。
func (runtime *ProjectRuntime) Store() *project.ProjectStore {
	if runtime == nil {
		return nil
	}
	return runtime.store
}

// Evidence 返回项目持有的审计 journal。
func (runtime *ProjectRuntime) Evidence() *turn.EvidenceStore {
	if runtime == nil {
		return nil
	}
	return runtime.journal
}

// Workspace 返回锚定在当前项目根目录的本地工具后端。
func (runtime *ProjectRuntime) Workspace() *builtins.Workspace {
	if runtime == nil {
		return nil
	}
	return runtime.workspace
}

// Tools 从已配置的项目 Provider 解析外部工具。
func (runtime *ProjectRuntime) Tools(ctx context.Context) ([]core.Tool, error) {
	if runtime == nil || runtime.tools == nil {
		return nil, nil
	}
	return runtime.tools.Tools(ctx)
}

// PersistState 提交 worker 持有的会话快照并更新可重建的项目索引。
// worker 执行过程中调用本方法也是安全的。
func (runtime *ProjectRuntime) PersistState(session *sessionstate.Session) error {
	if runtime == nil || session == nil {
		return fmt.Errorf("session state is invalid")
	}
	runtime.commitMu.Lock()
	defer runtime.commitMu.Unlock()
	return runtime.commitSessionLocked(sessionstate.CloneSession(session))
}

// commitSessionLocked 重建项目摘要条目，并在发布更新后的项目元数据前，
// 与会话数据一并原子保存。
func (runtime *ProjectRuntime) commitSessionLocked(session *sessionstate.Session) error {
	runtime.mu.RLock()
	if runtime.project == nil {
		runtime.mu.RUnlock()
		return fmt.Errorf("project is unavailable")
	}
	project := projectmodel.CloneProject(runtime.project)
	runtime.mu.RUnlock()
	found := false
	for index, summary := range project.Sessions {
		if summary.ID == session.ID {
			project.Sessions[index] = projectmodel.SessionSummary{ID: session.ID, UpdatedAt: session.UpdatedAt}
			found = true
			break
		}
	}
	if !found {
		project.Sessions = append(project.Sessions, projectmodel.SessionSummary{ID: session.ID, UpdatedAt: session.UpdatedAt})
	}
	project.UpdatedAt = time.Now().UTC()
	if err := runtime.store.CommitSession(session, project); err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.project = project
	runtime.mu.Unlock()
	return nil
}

// Close 停止全部 worker、持久化最终快照，然后按顺序释放 conversation、外部工具、
// 证据和项目存储。并发调用方会等待并得到首次关闭的结果。
func (runtime *ProjectRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.lifecycle.Lock()
	if runtime.closed {
		done := runtime.closeDone
		runtime.lifecycle.Unlock()
		<-done
		runtime.mu.RLock()
		err := runtime.closeErr
		runtime.mu.RUnlock()
		return err
	}
	runtime.mu.Lock()
	runtime.closed = true
	runtime.cancel()
	sessions := make([]*sessionRuntime, 0, len(runtime.sessions))
	for _, session := range runtime.sessions {
		sessions = append(sessions, session)
	}
	runtime.mu.Unlock()
	runtime.lifecycle.Unlock()

	for _, session := range sessions {
		session.worker.Stop()
	}
	var closeErr error
	for _, session := range sessions {
		<-session.worker.Done()
		if err := runtime.PersistState(session.worker.Snapshot()); closeErr == nil && err != nil {
			closeErr = err
		}
		if err := session.conversation.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	if closer, ok := runtime.tools.(core.ToolCloser); ok {
		if err := closer.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	if err := runtime.journal.Close(); closeErr == nil && err != nil {
		closeErr = err
	}
	if err := runtime.store.Close(); closeErr == nil && err != nil {
		closeErr = err
	}
	runtime.mu.Lock()
	runtime.closeErr = closeErr
	runtime.mu.Unlock()
	close(runtime.closeDone)
	return closeErr
}
