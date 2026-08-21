// Command pentgo 通过精简的进程级命令接口启动项目终端智能体。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"pentgo/internal/app"
	"pentgo/internal/cli"
	"pentgo/internal/config"
)

// main 启动项目终端，并将进程信号收敛到运行时根 context。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runCommand(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// runREPL 加载配置并启动项目/会话终端入口。
func runREPL(ctx context.Context, input io.Reader, stdout, stderr io.Writer) int {
	return runCommand(ctx, nil, input, stdout, stderr)
}

// command 是解析后的进程启动参数，项目操作统一在 TUI 中完成。
type command struct {
	resume bool
}

// usage 在启动 TUI 前为格式错误的进程参数输出用法说明。
const usage = "usage: pentgo [resume]"

// runCommand 规范化进程依赖、打开当前工作区，并将终端取消映射为约定的 130 退出码。
func runCommand(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) int {
	command, err := parseCommand(args)
	if err != nil {
		fmt.Fprintln(stderr, usage)
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "warning: config load failed; using defaults:", err)
		cfg = config.Default()
	}
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "error: resolve working directory:", err)
		return 1
	}
	runtime := app.New(cfg, workingDir, app.Dependencies{SkillsFS: os.DirFS(filepath.Join(workingDir, "skills"))})
	terminal := cli.NewRuntimeTerminal(runtime, input, stdout)
	var runErr error
	if command.resume {
		runErr = terminal.Resume(ctx)
	} else {
		runErr = terminal.Run(ctx)
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return 130
		}
		fmt.Fprintln(stderr, "error:", runErr)
		return 1
	}
	return 0
}

// parseCommand 只接受默认启动或 `resume`；项目操作由 TUI 命令执行。
func parseCommand(args []string) (command, error) {
	switch len(args) {
	case 0:
		return command{}, nil
	case 1:
		if args[0] == "resume" {
			return command{resume: true}, nil
		}
	}
	return command{}, fmt.Errorf("invalid command")
}
