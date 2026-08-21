package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pentgo/internal/domain"
)

// TurnFunc 由 worker goroutine 调用。回调执行期间 session 指针归该 goroutine 独占，
// 不得泄漏到其外部。
type TurnFunc func(context.Context, *domain.Session, string) error

// SessionWorker 在自身 goroutine 上串行处理一个会话的全部可变状态变更。
// 其他 goroutine 只能提交请求、读取克隆快照和消费有界进度事件。
type SessionWorker struct {
	session  *domain.Session
	turn     TurnFunc
	input    chan workerRequest
	cancel   context.CancelFunc
	done     chan struct{}
	events   chan Event
	snapshot atomic.Pointer[domain.Session]
	closed   atomic.Bool
	stateMu  sync.Mutex
}

// workerRequest 将 turn 提交或重命名操作传递给 worker 独占的事件循环。
// done 为带缓冲通道，避免关闭时调用方被遗留阻塞。
type workerRequest struct {
	ctx     context.Context
	message string
	name    string
	rename  bool
	done    chan error
}

// NewSessionWorker 从私有会话副本创建 worker。
func NewSessionWorker(parent context.Context, session *domain.Session, turn TurnFunc) (*SessionWorker, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if turn == nil {
		return nil, fmt.Errorf("session turn callback is nil")
	}
	if parent == nil {
		parent = context.Background()
	}
	workerContext, cancel := context.WithCancel(parent)
	worker := &SessionWorker{
		session: domain.CloneSession(session),
		turn:    turn,
		input:   make(chan workerRequest, 16),
		cancel:  cancel,
		done:    make(chan struct{}),
		events:  make(chan Event, 64),
	}
	worker.snapshot.Store(domain.CloneSession(worker.session))
	go worker.run(workerContext)
	return worker, nil
}

// Submit 将用户消息加入队列，并返回在该 turn 结束后写入结果的通道。
// worker 停止或调用方 context 过期时拒绝请求。
func (worker *SessionWorker) Submit(ctx context.Context, message string) <-chan error {
	done := make(chan error, 1)
	if worker == nil {
		done <- fmt.Errorf("session worker is nil")
		return done
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := workerRequest{ctx: ctx, message: message, done: done}
	worker.stateMu.Lock()
	if worker.closed.Load() {
		worker.stateMu.Unlock()
		done <- fmt.Errorf("session %q is closed", worker.session.ID)
		return done
	}
	select {
	case worker.input <- request:
		worker.stateMu.Unlock()
	case <-worker.done:
		worker.stateMu.Unlock()
		done <- fmt.Errorf("session %q is closed", worker.session.ID)
	case <-ctx.Done():
		worker.stateMu.Unlock()
		done <- ctx.Err()
	}
	return done
}

// Rename 将名称变更加入队列，使其遵循与 turn 相同的状态所有权约束。
func (worker *SessionWorker) Rename(name string) <-chan error {
	done := make(chan error, 1)
	if worker == nil {
		done <- fmt.Errorf("session worker is nil")
		return done
	}
	name = strings.TrimSpace(name)
	if name == "" {
		done <- fmt.Errorf("session name is empty")
		return done
	}
	request := workerRequest{name: name, rename: true, done: done}
	worker.stateMu.Lock()
	if worker.closed.Load() {
		worker.stateMu.Unlock()
		done <- fmt.Errorf("session %q is closed", worker.session.ID)
		return done
	}
	select {
	case worker.input <- request:
		worker.stateMu.Unlock()
	case <-worker.done:
		worker.stateMu.Unlock()
		done <- fmt.Errorf("session %q is closed", worker.session.ID)
	}
	return done
}

// Snapshot 返回原子发布的副本，可供并发读取。
func (worker *SessionWorker) Snapshot() *domain.Session {
	if worker == nil {
		return nil
	}
	return domain.CloneSession(worker.snapshot.Load())
}

// Events 暴露非持久化进度通知的有界事件流。
func (worker *SessionWorker) Events() <-chan Event {
	if worker == nil {
		return nil
	}
	return worker.events
}

// Done 在输入请求处理完毕且事件流关闭后关闭。
func (worker *SessionWorker) Done() <-chan struct{} {
	if worker == nil {
		return nil
	}
	return worker.done
}

// SessionID 返回 worker 所属会话的不可变标识。
func (worker *SessionWorker) SessionID() string {
	if worker == nil || worker.session == nil {
		return ""
	}
	return worker.session.ID
}

// Stop 停止 worker。项目关闭后会话仍可从持久化 transcript 重新打开。
func (worker *SessionWorker) Stop() {
	if worker == nil || worker.cancel == nil {
		return
	}
	worker.cancel()
}

// Emit 允许在该 worker 内执行的应用服务发布工具和助手事件，
// 而不转移事件通道的所有权。
func (worker *SessionWorker) Emit(event Event) {
	if worker == nil {
		return
	}
	worker.publish(event)
}

// PublishSnapshot 无需等待模型 turn 结束即可发布当前 worker 持有的状态。
// 应用服务在工具或事实完成持久化变更后调用它。
func (worker *SessionWorker) PublishSnapshot() {
	if worker == nil {
		return
	}
	worker.publishSnapshot()
}

// run 是会话状态的唯一写入方，按顺序处理队列请求，保证每个会话最多只有一个活动模型 turn。
func (worker *SessionWorker) run(ctx context.Context) {
	defer worker.finish()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-worker.input:
			if request.rename {
				err := worker.session.Rename(request.name)
				worker.publishSnapshot()
				request.done <- err
				continue
			}
			if err := ctx.Err(); err != nil {
				request.done <- err
				return
			}
			if err := request.ctx.Err(); err != nil {
				request.done <- err
				continue
			}
			turnContext, cancel := context.WithCancel(request.ctx)
			stopWorker := context.AfterFunc(ctx, cancel)
			worker.publish(Event{SessionID: worker.session.ID, Kind: EventTurnStarted, Message: request.message})
			err := worker.turn(turnContext, worker.session, request.message)
			stopWorker()
			cancel()
			if transitionErr := worker.interruptOrFailAfterError(turnContext, err); transitionErr != nil && err == nil {
				err = transitionErr
			}
			worker.publishSnapshot()
			request.done <- err
		}
	}
}

// interruptOrFailAfterError 在回调异常退出时，将未完成的运行中 turn 转换为正确的持久化终止状态。
func (worker *SessionWorker) interruptOrFailAfterError(turnContext context.Context, turnErr error) error {
	if worker.session == nil || worker.session.ActiveTurn == nil || worker.session.ActiveTurn.Status != domain.TurnRunning {
		return nil
	}
	if turnErr == nil && turnContext.Err() == nil {
		return nil
	}
	finishedAt := time.Now().UTC()
	reason := "turn failed"
	if errors.Is(turnErr, context.Canceled) || errors.Is(turnErr, context.DeadlineExceeded) || turnContext.Err() != nil {
		reason = "turn interrupted"
		if err := worker.session.InterruptTurn(worker.session.ActiveTurn.ID, reason, finishedAt); err != nil {
			return err
		}
		worker.publish(Event{SessionID: worker.session.ID, TurnID: worker.session.ActiveTurn.ID, Kind: EventTurnFinished, Message: reason})
		return nil
	}
	if err := worker.session.FailTurn(worker.session.ActiveTurn.ID, reason, finishedAt); err != nil {
		return err
	}
	worker.publish(Event{SessionID: worker.session.ID, TurnID: worker.session.ActiveTurn.ID, Kind: EventTurnFailed, Message: turnErr.Error()})
	return nil
}

// finish 完成最终状态迁移、拒绝队列中未处理的工作，并且只关闭一次可观察通道。
func (worker *SessionWorker) finish() {
	worker.closed.Store(true)

	if worker.session.ActiveTurn != nil && worker.session.ActiveTurn.Status == domain.TurnRunning {
		turnID := worker.session.ActiveTurn.ID
		if err := worker.session.InterruptTurn(turnID, "runtime stopped", time.Now().UTC()); err == nil {
			worker.publish(Event{SessionID: worker.session.ID, TurnID: turnID, Kind: EventTurnFinished, Message: "runtime stopped"})
		}
	}
	worker.publishSnapshot()
	for {
		select {
		case request := <-worker.input:
			request.done <- context.Canceled
		default:
			close(worker.events)
			close(worker.done)
			return
		}
	}
}

// publishSnapshot 在发布前复制 worker 独占状态。
func (worker *SessionWorker) publishSnapshot() {
	worker.snapshot.Store(domain.CloneSession(worker.session))
}

// publish 绝不阻塞模型执行。当缓慢的 UI 填满缓冲区时，丢弃最旧的进度事件；
// 持久化 transcript 数据不受影响。
func (worker *SessionWorker) publish(event Event) {
	select {
	case worker.events <- event:
	default:
		select {
		case <-worker.events:
		default:
		}
		select {
		case worker.events <- event:
		default:
		}
	}
}
