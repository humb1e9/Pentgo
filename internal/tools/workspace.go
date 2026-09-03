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

// Workspace provides unrestricted local file tools. root is the default base for relative paths and shell commands.
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

// LsInfo accepts absolute and workspace-relative paths.
func (workspace *Workspace) LsInfo(ctx context.Context, request *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := workspace.resolveExternal(request.Path)
	if err != nil {
		return nil, err
	}
	return workspace.local.LsInfo(ctx, &filesystem.LsInfoRequest{Path: path})
}

// Read accepts absolute and workspace-relative paths so skills can read referenced files outside a project.
func (workspace *Workspace) Read(ctx context.Context, request *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path, err := workspace.resolveExternal(request.FilePath)
	if err != nil {
		return nil, err
	}
	return workspace.local.Read(ctx, &filesystem.ReadRequest{FilePath: path, Offset: request.Offset, Limit: request.Limit})
}

// GrepRaw accepts absolute and workspace-relative paths without modifying the request.
func (workspace *Workspace) GrepRaw(ctx context.Context, request *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	path, err := workspace.resolveExternal(request.Path)
	if err != nil {
		return nil, err
	}
	copy := *request
	copy.Path = path
	return workspace.local.GrepRaw(ctx, &copy)
}

// GlobInfo accepts absolute and workspace-relative paths without modifying the request.
func (workspace *Workspace) GlobInfo(ctx context.Context, request *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := workspace.resolveExternal(request.Path)
	if err != nil {
		return nil, err
	}
	copy := *request
	copy.Path = path
	return workspace.local.GlobInfo(ctx, &copy)
}

// Write accepts absolute and workspace-relative paths.
func (workspace *Workspace) Write(ctx context.Context, request *filesystem.WriteRequest) error {
	path, err := workspace.resolveExternal(request.FilePath)
	if err != nil {
		return err
	}
	return workspace.local.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: request.Content})
}

// Edit accepts absolute and workspace-relative paths.
func (workspace *Workspace) Edit(ctx context.Context, request *filesystem.EditRequest) error {
	path, err := workspace.resolveExternal(request.FilePath)
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

// resolveExternal resolves absolute and workspace-relative paths without enforcing a workspace boundary.
func (workspace *Workspace) resolveExternal(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("file path is required")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(workspace.root, value)
	}
	resolved, err := resolveExisting(filepath.Clean(value))
	if err != nil {
		return "", err
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

// shellQuote 生成 POSIX 单引号形式的 Shell 字面量。
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

var _ filesystem.Backend = (*Workspace)(nil)
var _ filesystem.Shell = (*Workspace)(nil)
