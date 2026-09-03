// Command pentgo 通过精简的进程级命令接口启动项目终端智能体。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"pentgo/app"
	"pentgo/terminal"
)

// main 启动项目终端，并将进程信号收敛到运行时根 context。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runCommand(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// usage 在启动 TUI 前为格式错误的进程参数输出用法说明。
const usage = "usage: pentgo [resume]"

// runCommand 规范化进程依赖、打开当前工作区，并将终端取消映射为约定的 130 退出码。
func runCommand(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) int {
	resume, err := parseCommand(args)
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
	cfg, err := app.Load()
	if err != nil {
		if errors.Is(err, app.ErrConfigCreated) {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stderr, "error: load config:", err)
		return 1
	}
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "error: resolve working directory:", err)
		return 1
	}
	skillsDir, err := app.SkillsDir()
	if err != nil {
		fmt.Fprintln(stderr, "error: resolve skills directory:", err)
		return 1
	}
	runtime := app.NewApplication(cfg, workingDir, os.DirFS(skillsDir))
	terminal := terminal.NewRuntimeTerminal(runtime, input, stdout)
	var runErr error
	if resume {
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
func parseCommand(args []string) (resume bool, err error) {
	switch {
	case len(args) == 0:
		return false, nil
	case len(args) == 1 && args[0] == "resume":
		return true, nil
	}
	return false, fmt.Errorf("invalid command")
}
