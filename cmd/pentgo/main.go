package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"pentgo/internal/app"
	"pentgo/internal/config"
	"pentgo/internal/terminal"
)

// main 仅启动终端交互，并将 SIGINT 交给当前 engagement 的取消逻辑。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	os.Exit(runREPL(ctx, os.Stdin, os.Stdout, os.Stderr, interrupts))
}

// runREPL 加载配置并启动唯一的自然语言 Recon 终端入口。
func runREPL(ctx context.Context, input io.Reader, stdout, stderr io.Writer, signals <-chan os.Signal) int {
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
	service := app.NewService(cfg, app.Dependencies{})
	if err := terminal.New(input, stdout, service, signals).Run(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 130
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
