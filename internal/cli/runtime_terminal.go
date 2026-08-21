package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pentgo/internal/app"
)

// RuntimeTerminal 持有终端输入输出和程序生命周期，terminalModel 仅持有 Bubble Tea 显示状态。
type RuntimeTerminal struct {
	coordinator *app.Coordinator
	input       io.Reader
	output      io.Writer
}

// NewRuntimeTerminal 提供非 nil 且便于测试的输入输出默认值。
func NewRuntimeTerminal(coordinator *app.Coordinator, input io.Reader, output io.Writer) *RuntimeTerminal {
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}
	return &RuntimeTerminal{coordinator: coordinator, input: input, output: output}
}

// Run 打开或恢复当前工作区，启动 Bubble Tea，并在 UI 退出时关闭项目资源。
// 使用真实标准输入输出时启用备用屏幕。
func (terminal *RuntimeTerminal) Run(ctx context.Context) error {
	return terminal.run(ctx, true, "")
}

// Resume 在 TUI 外列出已有会话并选择一条恢复。
func (terminal *RuntimeTerminal) Resume(ctx context.Context) error {
	if terminal == nil || terminal.coordinator == nil {
		return fmt.Errorf("nil runtime terminal")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := terminal.coordinator.OpenCurrentProject(ctx); err != nil {
		return err
	}
	sessionID, err := terminal.selectResumeSession()
	if err != nil {
		_ = terminal.coordinator.CloseProject()
		return err
	}
	return terminal.run(ctx, false, sessionID)
}

// run 按启动模式准备工作区，再启动 Bubble Tea。
func (terminal *RuntimeTerminal) run(ctx context.Context, createWorkspace bool, sessionID string) error {
	if terminal == nil || terminal.coordinator == nil {
		return fmt.Errorf("nil runtime terminal")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runtimeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	defer terminal.coordinator.CloseProject()
	focused, err := terminal.restoreCurrent(runtimeContext, createWorkspace, sessionID)
	if err != nil {
		return err
	}
	options := []tea.ProgramOption{tea.WithInput(terminal.input), tea.WithOutput(terminal.output)}
	if terminal.input == os.Stdin && terminal.output == os.Stdout {
		options = append(options, tea.WithAltScreen())
	}
	result, err := tea.NewProgram(newTerminalModel(runtimeContext, terminal.coordinator, focused), options...).Run()
	if err != nil {
		return err
	}
	if model, ok := result.(*terminalModel); ok {
		return model.err
	}
	return nil
}

// restoreCurrent 创建新会话，或恢复外层已选定的会话。
func (terminal *RuntimeTerminal) restoreCurrent(ctx context.Context, createWorkspace bool, sessionID string) (string, error) {
	if createWorkspace {
		if _, _, err := terminal.coordinator.OpenOrCreateWorkspace(ctx); err != nil {
			return "", err
		}
	} else if _, err := terminal.coordinator.OpenCurrentProject(ctx); err != nil {
		return "", err
	}
	if sessionID != "" {
		session, err := terminal.coordinator.ResumeSession(sessionID)
		if err != nil {
			return "", err
		}
		return session.ID, nil
	}
	session, err := terminal.coordinator.NewSession("交互会话")
	if err != nil {
		return "", err
	}
	return session.ID, nil
}

// selectResumeSession 在启动 TUI 前选择要回放的会话，保持 TUI 只负责当前对话。
func (terminal *RuntimeTerminal) selectResumeSession() (string, error) {
	sessions := terminal.coordinator.Sessions()
	if len(sessions) == 0 {
		return "", fmt.Errorf("没有可恢复的会话")
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].ID > sessions[j].ID
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	reader, ok := terminal.input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(terminal.input)
		terminal.input = reader
	}
	fmt.Fprintln(terminal.output, "恢复会话")
	for index, session := range sessions {
		name := strings.TrimSpace(session.Name)
		if name == "" {
			name = session.ID
		}
		fmt.Fprintf(terminal.output, "%d. %s  %s  %d turns\n", index+1, name, session.ID, session.Turns)
	}
	fmt.Fprint(terminal.output, "选择会话 [1]: ")
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("读取会话选择: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return sessions[0].ID, nil
	}
	if index, parseErr := strconv.Atoi(choice); parseErr == nil {
		if index >= 1 && index <= len(sessions) {
			return sessions[index-1].ID, nil
		}
		return "", fmt.Errorf("会话编号必须在 1 到 %d 之间", len(sessions))
	}
	for _, session := range sessions {
		if session.ID == choice {
			return session.ID, nil
		}
	}
	return "", fmt.Errorf("未找到会话 %q", choice)
}
