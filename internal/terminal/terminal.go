package terminal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"pentgo/internal/app"
	"pentgo/internal/runtime"
)

// EngagementRunner 定义终端运行一个 engagement 所需的应用服务边界。
type EngagementRunner interface {
	Run(context.Context, app.Request, func(app.Event)) (app.Result, error)
}

// Task 保存从自然语言输入提取出的目标和完整任务意图。
type Task struct {
	Target runtime.Target
	Intent string
}

// Terminal 管理串行的自然语言 Agent REPL 会话。
type Terminal struct {
	input      io.Reader
	output     io.Writer
	runner     EngagementRunner
	signals    <-chan os.Signal
	outputRoot string

	outputMu sync.Mutex
	last     app.Result
	hasLast  bool
}

// New 使用当前目录作为 artifact 输出根目录创建终端。
func New(input io.Reader, output io.Writer, runner EngagementRunner, signals <-chan os.Signal) *Terminal {
	return NewWithOutputRoot(input, output, runner, signals, ".")
}

// NewWithOutputRoot 使用指定 artifact 输出根目录创建终端。
func NewWithOutputRoot(input io.Reader, output io.Writer, runner EngagementRunner, signals <-chan os.Signal, outputRoot string) *Terminal {
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}
	if strings.TrimSpace(outputRoot) == "" {
		outputRoot = "."
	}
	return &Terminal{input: input, output: output, runner: runner, signals: signals, outputRoot: outputRoot}
}

// ParseTask 提取首个 HTTP(S) URL 或裸域名，并保留完整自然语言意图。
func ParseTask(line string) (Task, error) {
	intent := strings.TrimSpace(line)
	if intent == "" {
		return Task{}, errors.New("task is empty")
	}
	target, err := runtime.ParseTarget(intent)
	if err != nil {
		return Task{}, fmt.Errorf("task must contain an HTTP(S) URL or domain: %w", err)
	}
	return Task{Target: target, Intent: intent}, nil
}

// Run 读取自然语言任务和内建命令，并串行执行 engagement。
func (terminal *Terminal) Run(ctx context.Context) error {
	if terminal == nil {
		return errors.New("nil terminal")
	}
	if terminal.runner == nil {
		return errors.New("nil engagement runner")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lines, readerDone := readLines(terminal.input)
	signals := terminal.signals
	terminal.printPrompt()

	var activeDone <-chan terminalRunResult
	var activeCancel context.CancelFunc
	readerClosed := false
	for {
		if activeDone == nil {
			if readerClosed {
				return <-readerDone
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case _, ok := <-signals:
				if !ok {
					signals = nil
					continue
				}
				terminal.writeString("No active engagement to cancel.\n")
				terminal.printPrompt()
			case line, ok := <-lines:
				if !ok {
					readerClosed = true
					lines = nil
					continue
				}
				if terminal.handleIdleLine(ctx, line, &activeDone, &activeCancel) {
					return nil
				}
			}
			continue
		}

		select {
		case completed := <-activeDone:
			activeCancel()
			activeDone = nil
			activeCancel = nil
			terminal.recordAndPrint(completed)
			if err := ctx.Err(); err != nil {
				return err
			}
			terminal.printPrompt()
		case _, ok := <-signals:
			if !ok {
				signals = nil
				continue
			}
			activeCancel()
			terminal.writeString("Cancelling current engagement.\n")
		case <-ctx.Done():
			activeCancel()
			completed := <-activeDone
			terminal.recordAndPrint(completed)
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				readerClosed = true
				lines = nil
				continue
			}
			if terminal.handleActiveLine(line, activeCancel) {
				completed := <-activeDone
				terminal.recordAndPrint(completed)
				return nil
			}
		}
	}
}

func (terminal *Terminal) handleIdleLine(ctx context.Context, line string, activeDone *<-chan terminalRunResult, activeCancel *context.CancelFunc) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		terminal.printPrompt()
		return false
	}
	switch line {
	case "/quit", "/exit":
		return true
	case "/help":
		terminal.writeString("Enter a natural language task containing an HTTP(S) URL or domain.\n")
		terminal.writeString("Commands: /help, /status, /cancel, /quit, /exit\n")
		terminal.printPrompt()
		return false
	case "/status":
		terminal.printStatus()
		terminal.printPrompt()
		return false
	case "/cancel":
		terminal.writeString("No active engagement to cancel.\n")
		terminal.printPrompt()
		return false
	}
	if strings.HasPrefix(line, "/") {
		terminal.writeString("error: unknown command\n")
		terminal.printPrompt()
		return false
	}
	task, err := ParseTask(line)
	if err != nil {
		terminal.writeString("error: " + err.Error() + "\n")
		terminal.printPrompt()
		return false
	}
	runContext, cancel := context.WithCancel(ctx)
	done := make(chan terminalRunResult, 1)
	*activeCancel = cancel
	*activeDone = done
	go func() {
		result, runErr := terminal.runner.Run(runContext, app.Request{Target: task.Target, Intent: task.Intent, OutputRoot: terminal.outputRoot}, terminal.handleEvent)
		done <- terminalRunResult{result: result, err: runErr}
	}()
	return false
}

func (terminal *Terminal) handleActiveLine(line string, cancel context.CancelFunc) bool {
	switch strings.TrimSpace(line) {
	case "/cancel":
		cancel()
		terminal.writeString("Cancelling current engagement.\n")
	case "/quit", "/exit":
		cancel()
		terminal.writeString("Cancelling current engagement.\n")
		return true
	case "/status":
		terminal.writeString("Engagement is running.\n")
	case "/help":
		terminal.writeString("Commands while running: /status, /cancel, /quit, /exit\n")
	default:
		terminal.writeString("A task is already running; use /cancel before starting another task.\n")
	}
	return false
}

func (terminal *Terminal) handleEvent(event app.Event) {
	if message := strings.TrimSpace(event.Message); message != "" {
		terminal.writeString(message + "\n")
	}
}

func (terminal *Terminal) recordAndPrint(completed terminalRunResult) {
	terminal.last = completed.result
	terminal.hasLast = true
	terminal.printRunResult(completed)
}

func (terminal *Terminal) printRunResult(completed terminalRunResult) {
	if completed.result.Artifacts.SessionJSON != "" {
		terminal.writeString("Session: " + completed.result.Artifacts.SessionJSON + "\n")
	}
	if completed.result.Artifacts.Markdown != "" {
		terminal.writeString("Report: " + completed.result.Artifacts.Markdown + "\n")
	}
	if completed.result.Session != nil && completed.result.Session.Status == runtime.SessionCancelled {
		terminal.writeString("Engagement cancelled.\n")
		return
	}
	if isCancellation(completed.err) || isCancellation(completed.result.RunError) {
		terminal.writeString("Engagement cancelled.\n")
		return
	}
	if completed.err != nil {
		terminal.writeString("error: " + completed.err.Error() + "\n")
		return
	}
	if completed.result.RunError != nil {
		terminal.writeString("error: engagement: " + completed.result.RunError.Error() + "\n")
		return
	}
	terminal.writeString("Engagement complete.\n")
}

func (terminal *Terminal) printStatus() {
	if !terminal.hasLast || terminal.last.Session == nil {
		terminal.writeString("No engagement has run.\n")
		return
	}
	session := terminal.last.Session
	terminal.writeString("Engagement: " + session.ID + "\n")
	terminal.writeString("Status: " + string(session.Status) + "\n")
	if terminal.last.Artifacts.Directory != "" {
		terminal.writeString("Artifacts: " + terminal.last.Artifacts.Directory + "\n")
	}
}

func (terminal *Terminal) printPrompt() {
	terminal.writeString("pentgo> ")
}

func (terminal *Terminal) writeString(value string) {
	if terminal == nil || terminal.output == nil {
		return
	}
	terminal.outputMu.Lock()
	defer terminal.outputMu.Unlock()
	_, _ = io.WriteString(terminal.output, value)
}

type terminalRunResult struct {
	result app.Result
	err    error
}

func readLines(input io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	done := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 1024), 256*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		done <- scanner.Err()
	}()
	return lines, done
}

func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
