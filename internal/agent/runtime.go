package agent

import (
	"context"
	"errors"
	"fmt"
	"pentgo/internal/tools"
	"strings"
	"sync"
	"time"

	"pentgo/internal/evidence"
	projectmodel "pentgo/internal/project"
	sessionstate "pentgo/internal/session"
	"pentgo/internal/storage"
	builtins "pentgo/internal/tools"
)

// ProjectRuntime 是项目级资源的唯一所有者。会话状态保留在各自的 sessionstate.Worker 中；
// 此类型仅保存不可变引用和已发布快照。
type ProjectRuntime struct {
	mu        sync.RWMutex
	lifecycle sync.Mutex
	commitMu  sync.Mutex
	store     *storage.ProjectStore
	project   *projectmodel.Project
	facts     *ProjectFactLedger
	journal   *evidence.EvidenceStore
	workspace *builtins.Workspace
	tools     tools.Provider
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

// OpenProjectRuntime 按 Close 的逆序加载项目资源，并在 Evidence 中脱敏可选敏感值。
// 调用方必须先设置 turn handler，之后才能打开会话。
func OpenProjectRuntime(ctx context.Context, store *storage.ProjectStore, portsTools tools.Provider, secrets ...string) (*ProjectRuntime, error) {
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
	journal, err := evidence.OpenEvidenceStore(store.DatabasePath(), secrets...)
	if err != nil {
		return nil, err
	}
	facts := NewProjectLedger(repository, journal)
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
		journal:   journal,
		workspace: workspace,
		tools:     portsTools,
		sessions:  make(map[string]*sessionRuntime),
		ctx:       runtimeContext,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}, nil
}

// SetToolProvider 必须在任一会话打开前安装项目级外部工具，
// 确保恢复会话和新建会话看到稳定的 Provider。
func (runtime *ProjectRuntime) SetToolProvider(portsTools tools.Provider) error {
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
	runtime.tools = portsTools
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
func (runtime *ProjectRuntime) NewSession(intent string) (*sessionstate.Session, error) {
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
func (runtime *ProjectRuntime) Emit(sessionID string, event sessionstate.Event) {
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

// Evidence 返回项目持有的审计 journal。
func (runtime *ProjectRuntime) Evidence() *evidence.EvidenceStore {
	if runtime == nil {
		return nil
	}
	return runtime.journal
}

// Tools 从已配置的项目 Provider 解析外部工具。
func (runtime *ProjectRuntime) Tools(ctx context.Context) ([]tools.Tool, error) {
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
	if closer, ok := runtime.tools.(tools.Closer); ok {
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
