package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	local "github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk/filesystem"
)

// Workspace 将 Eino 的文件系统和 Shell 工具限制在单个项目工作区内。
// 每个文件请求都会先基于 root 解析，再交给本地后端执行。
type Workspace struct {
	root  string
	local *local.Local
}

// IsName 判断名称是否由 Eino 内置本地后端保留。
func IsName(name string) bool {
	switch name {
	case "ls", "read_file", "write_file", "edit_file", "glob", "grep", "execute":
		return true
	default:
		return false
	}
}

// NewWorkspace 在创建后端前规范化并校验 root。
func NewWorkspace(root string) (*Workspace, error) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory")
	}
	backend, err := local.NewBackend(context.Background(), &local.Config{})
	if err != nil {
		return nil, fmt.Errorf("create local filesystem backend: %w", err)
	}
	return &Workspace{root: root, local: backend}, nil
}

// LsInfo 在项目工作区内解析路径后列出文件信息。
func (workspace *Workspace) LsInfo(ctx context.Context, request *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := workspace.resolve(request.Path)
	if err != nil {
		return nil, err
	}
	return workspace.local.LsInfo(ctx, &filesystem.LsInfoRequest{Path: path})
}

// Read 读取相对于工作区的文件内容。
func (workspace *Workspace) Read(ctx context.Context, request *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path, err := workspace.resolve(request.FilePath)
	if err != nil {
		return nil, err
	}
	return workspace.local.Read(ctx, &filesystem.ReadRequest{FilePath: path, Offset: request.Offset, Limit: request.Limit})
}

// GrepRaw 搜索相对于工作区的路径，且不修改传入请求。
func (workspace *Workspace) GrepRaw(ctx context.Context, request *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	path, err := workspace.resolve(request.Path)
	if err != nil {
		return nil, err
	}
	copy := *request
	copy.Path = path
	return workspace.local.GrepRaw(ctx, &copy)
}

// GlobInfo 展开相对于工作区的路径，且不修改传入请求。
func (workspace *Workspace) GlobInfo(ctx context.Context, request *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := workspace.resolve(request.Path)
	if err != nil {
		return nil, err
	}
	copy := *request
	copy.Path = path
	return workspace.local.GlobInfo(ctx, &copy)
}

// Write 仅向已解析的工作区相对路径写入内容。
func (workspace *Workspace) Write(ctx context.Context, request *filesystem.WriteRequest) error {
	path, err := workspace.resolve(request.FilePath)
	if err != nil {
		return err
	}
	return workspace.local.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: request.Content})
}

// Edit 仅在校验目标路径后执行 Eino 编辑操作。
func (workspace *Workspace) Edit(ctx context.Context, request *filesystem.EditRequest) error {
	path, err := workspace.resolve(request.FilePath)
	if err != nil {
		return err
	}
	copy := *request
	copy.FilePath = path
	return workspace.local.Edit(ctx, &copy)
}

// Execute 为每条命令添加经过 Shell 转义的工作区 cd 前缀，确保模型即使在命令中使用
// 相对路径，也始终从项目边界内开始执行。
func (workspace *Workspace) Execute(ctx context.Context, request *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if request == nil || strings.TrimSpace(request.Command) == "" {
		return nil, fmt.Errorf("command is required")
	}
	return workspace.local.Execute(ctx, &filesystem.ExecuteRequest{Command: "cd -- " + shellQuote(workspace.root) + " && " + request.Command, RunInBackendGround: request.RunInBackendGround})
}

// resolve 拒绝绝对路径、词法路径穿越和越出 root 的符号链接穿越。
// 同时支持写入操作中尚不存在的末级路径。
func (workspace *Workspace) resolve(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return workspace.root, nil
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("path must be relative to the workspace root")
	}
	candidate := filepath.Clean(filepath.Join(workspace.root, value))
	if !within(workspace.root, candidate) {
		return "", fmt.Errorf("path escapes the workspace root")
	}
	resolved, err := resolveExisting(candidate)
	if err != nil {
		return "", err
	}
	if !within(workspace.root, resolved) {
		return "", fmt.Errorf("path resolves outside the workspace root")
	}
	return resolved, nil
}

// resolveExisting 解析最深层的已存在父目录后还原缺失后缀，既支持后续写入，
// 也会校验已存在路径中的符号链接。
func resolveExisting(path string) (string, error) {
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve path symlinks: %w", err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append([]string{filepath.Base(path)}, missing...)
		path = parent
	}
}

// within 判断 path 是否为 root 本身或其子路径。
func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// shellQuote 生成 POSIX 单引号形式的 Shell 字面量。
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

var _ filesystem.Backend = (*Workspace)(nil)
var _ filesystem.Shell = (*Workspace)(nil)
