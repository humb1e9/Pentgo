package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TurnFunc is called by the worker goroutine. During a call the session pointer
// is owned by that goroutine and must not escape it.
type TurnFunc func(context.Context, *Session, string) error

// Worker serializes all mutable state for one session on its own goroutine.
type Worker struct {
	session  *Session
	turn     TurnFunc
	input    chan workerRequest
	cancel   context.CancelFunc
	done     chan struct{}
	events   chan Event
	snapshot atomic.Pointer[Session]
	closed   atomic.Bool
	stateMu  sync.Mutex
}

type workerRequest struct {
	ctx     context.Context
	message string
	name    string
	rename  bool
	done    chan error
}

// NewWorker creates a worker from a private session copy.
func NewWorker(parent context.Context, session *Session, turn TurnFunc) (*Worker, error) {
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
	worker := &Worker{
		session: CloneSession(session),
		turn:    turn,
		input:   make(chan workerRequest, 16),
		cancel:  cancel,
		done:    make(chan struct{}),
		events:  make(chan Event, 64),
	}
	worker.snapshot.Store(CloneSession(worker.session))
	go worker.run(workerContext)
	return worker, nil
}

// Submit queues a user message and returns a channel written once the turn
// finishes.
func (worker *Worker) Submit(ctx context.Context, message string) <-chan error {
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

// Rename queues a name change through the same state-ownership boundary.
func (worker *Worker) Rename(name string) <-chan error {
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

// Snapshot returns an atomically published copy safe for concurrent readers.
func (worker *Worker) Snapshot() *Session {
	if worker == nil {
		return nil
	}
	return CloneSession(worker.snapshot.Load())
}

// Events exposes the bounded progress event stream.
func (worker *Worker) Events() <-chan Event {
	if worker == nil {
		return nil
	}
	return worker.events
}

// Done closes after input requests are handled and the event stream is closed.
func (worker *Worker) Done() <-chan struct{} {
	if worker == nil {
		return nil
	}
	return worker.done
}

// SessionID returns the immutable worker session id.
func (worker *Worker) SessionID() string {
	if worker == nil || worker.session == nil {
		return ""
	}
	return worker.session.ID
}

// Stop stops the worker.
func (worker *Worker) Stop() {
	if worker == nil || worker.cancel == nil {
		return
	}
	worker.cancel()
}

// Emit publishes an event inside this worker without transferring event
// channel ownership.
func (worker *Worker) Emit(event Event) {
	if worker == nil {
		return
	}
	worker.publish(event)
}

// PublishSnapshot publishes the current worker-held state without waiting for
// a model turn to finish.
func (worker *Worker) PublishSnapshot() {
	if worker == nil {
		return
	}
	worker.publishSnapshot()
}

func (worker *Worker) run(ctx context.Context) {
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

func (worker *Worker) interruptOrFailAfterError(turnContext context.Context, turnErr error) error {
	if worker.session == nil || worker.session.ActiveTurn == nil || worker.session.ActiveTurn.Status != TurnRunning {
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

func (worker *Worker) finish() {
	worker.closed.Store(true)

	if worker.session.ActiveTurn != nil && worker.session.ActiveTurn.Status == TurnRunning {
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

func (worker *Worker) publishSnapshot() {
	worker.snapshot.Store(CloneSession(worker.session))
}

func (worker *Worker) publish(event Event) {
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
