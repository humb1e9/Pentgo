package exec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ExecutionStatus 描述一个代码块的终态。
type ExecutionStatus string

const (
	ExecutionSucceeded         ExecutionStatus = "succeeded"
	ExecutionFailed            ExecutionStatus = "failed"
	ExecutionTimedOut          ExecutionStatus = "timed_out"
	ExecutionCancelled         ExecutionStatus = "cancelled"
	ExecutionRepeatedOutput    ExecutionStatus = "repeated_output"
	ExecutionPreflightRejected ExecutionStatus = "preflight_rejected"
)

// ExecutorConfig 约束一次代码块执行的本地资源使用。
type ExecutorConfig struct {
	WorkDir             string
	Timeout             time.Duration
	MaxOutputBytes      int
	LineRepeatLimit     int
	ScanLineRepeatLimit int
}

// ExecutionInput 是一次模型回复中全部代码块的执行输入。
type ExecutionInput struct {
	SessionID string
	Target    string
	Turn      int
	Blocks    []PreflightResult
}

// ExecutionResult 是单个代码块的完整执行摘要。
type ExecutionResult struct {
	Block           CodeBlock       `json:"block"`
	Status          ExecutionStatus `json:"status"`
	ScriptPath      string          `json:"script_path,omitempty"`
	Interpreter     string          `json:"interpreter,omitempty"`
	ExitCode        int             `json:"exit_code"`
	Stdout          string          `json:"stdout,omitempty"`
	Stderr          string          `json:"stderr,omitempty"`
	StdoutTruncated bool            `json:"stdout_truncated,omitempty"`
	StderrTruncated bool            `json:"stderr_truncated,omitempty"`
	TimedOut        bool            `json:"timed_out,omitempty"`
	Cancelled       bool            `json:"cancelled,omitempty"`
	RepeatedOutput  bool            `json:"repeated_output,omitempty"`
	Error           string          `json:"error,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      time.Time       `json:"finished_at"`
}

// Executor 在一个 engagement 专属工作目录中运行模型代码。
type Executor struct {
	config           ExecutorConfig
	generatedMu      sync.Mutex
	generatedScripts []string
}

// NewExecutor 创建一个带默认限制的执行器。
func NewExecutor(config ExecutorConfig) *Executor {
	if config.WorkDir != "" {
		if absolute, err := filepath.Abs(config.WorkDir); err == nil {
			config.WorkDir = absolute
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Minute
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 65536
	}
	if config.LineRepeatLimit <= 0 {
		config.LineRepeatLimit = 100
	}
	if config.ScanLineRepeatLimit <= 0 {
		config.ScanLineRepeatLimit = 500
	}
	return &Executor{config: config}
}

// Execute runs preflighted blocks in source order.
func (executor *Executor) Execute(ctx context.Context, input ExecutionInput) []ExecutionResult {
	if executor == nil {
		return nil
	}
	results := make([]ExecutionResult, len(input.Blocks))
	for index, block := range input.Blocks {
		results[index] = executor.executeBlock(ctx, input, block)
	}
	return results
}

func (executor *Executor) executeBlock(ctx context.Context, input ExecutionInput, preflight PreflightResult) (result ExecutionResult) {
	result = ExecutionResult{Block: preflight.Block, ExitCode: -1, StartedAt: time.Now().UTC()}
	defer func() {
		result.FinishedAt = time.Now().UTC()
	}()
	if !preflight.Approved {
		result.Status = ExecutionPreflightRejected
		result.Error = preflight.Rejection
		return result
	}
	if err := os.MkdirAll(executor.config.WorkDir, 0o700); err != nil {
		result.Status = ExecutionFailed
		result.Error = fmt.Sprintf("create work directory: %v", err)
		return result
	}
	scriptPath, interpreter, arguments, err := executor.writeScript(input.Turn, preflight)
	if err != nil {
		result.Status = ExecutionFailed
		result.Error = err.Error()
		return result
	}
	result.ScriptPath = scriptPath
	result.Interpreter = interpreter
	command := exec.Command(interpreter, arguments...)
	command.Dir = executor.config.WorkDir
	command.Env = append(os.Environ(),
		"PENTGO_TARGET="+input.Target,
		"PENTGO_ENGAGEMENT_ID="+input.SessionID,
		"PENTGO_WORKDIR="+executor.config.WorkDir,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		result.Status = ExecutionFailed
		result.Error = fmt.Sprintf("open stdout: %v", err)
		return result
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		result.Status = ExecutionFailed
		result.Error = fmt.Sprintf("open stderr: %v", err)
		return result
	}
	if err := command.Start(); err != nil {
		result.Status = ExecutionFailed
		result.Error = fmt.Sprintf("start process: %v", err)
		return result
	}

	blockContext, cancel := context.WithTimeout(ctx, executor.config.Timeout)
	defer cancel()
	monitor := newRepeatMonitor(executor.config.LineRepeatLimit, executor.config.ScanLineRepeatLimit, cancel)
	stdoutCapture := outputCapture{limit: executor.config.MaxOutputBytes}
	stderrCapture := outputCapture{limit: executor.config.MaxOutputBytes}
	var outputWaitGroup sync.WaitGroup
	outputWaitGroup.Add(2)
	go func() {
		defer outputWaitGroup.Done()
		captureOutput(stdout, &stdoutCapture, monitor)
	}()
	go func() {
		defer outputWaitGroup.Done()
		captureOutput(stderr, &stderrCapture, monitor)
	}()

	stopWatcher := make(chan struct{})
	var terminatedByContext atomic.Bool
	go func() {
		select {
		case <-blockContext.Done():
			terminatedByContext.Store(true)
			killProcessGroup(command.Process)
		case <-stopWatcher:
		}
	}()
	outputWaitGroup.Wait()
	close(stopWatcher)
	waitErr := command.Wait()
	result.Stdout = stdoutCapture.value
	result.Stderr = stderrCapture.value
	result.StdoutTruncated = stdoutCapture.truncated
	result.StderrTruncated = stderrCapture.truncated
	result.ExitCode = exitCode(command.ProcessState)
	result.RepeatedOutput = monitor.repeated()
	if result.RepeatedOutput {
		result.Status = ExecutionRepeatedOutput
		result.Error = "INFINITE_LOOP: repeated output line limit reached"
		return result
	}
	if terminatedByContext.Load() {
		if blockContext.Err() == context.DeadlineExceeded {
			result.Status = ExecutionTimedOut
			result.TimedOut = true
			result.Error = "execution timeout"
			return result
		}
		result.Status = ExecutionCancelled
		result.Cancelled = true
		result.Error = "execution cancelled"
		return result
	}
	if waitErr != nil {
		result.Status = ExecutionFailed
		result.Error = waitErr.Error()
		return result
	}
	result.Status = ExecutionSucceeded
	return result
}

func (executor *Executor) writeScript(turn int, preflight PreflightResult) (string, string, []string, error) {
	extension := ".py"
	interpreter := "python3"
	arguments := []string{"-u"}
	if preflight.Block.Language == LanguageShell {
		extension = ".sh"
		interpreter = "bash"
		arguments = nil
	}
	filename := fmt.Sprintf("turn-%03d-block-%03d%s", turn, preflight.Block.Index, extension)
	path := filepath.Join(executor.config.WorkDir, filename)
	if err := os.WriteFile(path, []byte(preflight.Code), 0o600); err != nil {
		return "", "", nil, fmt.Errorf("write script: %w", err)
	}
	executor.registerGeneratedScript(path)
	arguments = append(arguments, path)
	return path, interpreter, arguments, nil
}

func (executor *Executor) registerGeneratedScript(path string) {
	executor.generatedMu.Lock()
	defer executor.generatedMu.Unlock()
	executor.generatedScripts = append(executor.generatedScripts, path)
}

func (executor *Executor) CleanupGeneratedScripts() error {
	if executor == nil {
		return nil
	}
	executor.generatedMu.Lock()
	defer executor.generatedMu.Unlock()
	for _, path := range executor.generatedScripts {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Runtime script %s: %w", path, err)
		}
	}
	executor.generatedScripts = nil
	return nil
}

func killProcessGroup(process *os.Process) {
	if process == nil {
		return
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil {
		_ = process.Kill()
	}
}

func exitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}

type outputCapture struct {
	limit     int
	value     string
	truncated bool
}

func (capture *outputCapture) append(line string) {
	if capture.truncated {
		return
	}
	remaining := capture.limit - len(capture.value)
	if remaining <= 0 {
		capture.truncated = true
		return
	}
	if len(line) > remaining {
		capture.value += line[:remaining]
		capture.truncated = true
		return
	}
	capture.value += line
}

func captureOutput(reader io.Reader, capture *outputCapture, monitor *repeatMonitor) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		capture.append(line + "\n")
		monitor.observe(line)
	}
}

type repeatMonitor struct {
	lineLimit     int
	scanLineLimit int
	cancel        context.CancelFunc
	mu            sync.Mutex
	last          string
	count         int
	stopped       bool
}

func newRepeatMonitor(lineLimit, scanLineLimit int, cancel context.CancelFunc) *repeatMonitor {
	return &repeatMonitor{lineLimit: lineLimit, scanLineLimit: scanLineLimit, cancel: cancel}
}

func (monitor *repeatMonitor) observe(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if line == monitor.last {
		monitor.count++
	} else {
		monitor.last = line
		monitor.count = 1
	}
	limit := monitor.lineLimit
	lower := strings.ToLower(line)
	if strings.Contains(lower, "http") || strings.Contains(lower, "scan") {
		limit = monitor.scanLineLimit
	}
	if !monitor.stopped && monitor.count >= limit {
		monitor.stopped = true
		monitor.cancel()
	}
}

func (monitor *repeatMonitor) repeated() bool {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return monitor.stopped
}
