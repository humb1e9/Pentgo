package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/adk/filesystem"
)

const workspaceToolOutputLimit = 64 * 1024

// NewTools returns the deterministic set of host-callable workspace actions.
// A nil workspace produces wrappers that return validation errors rather than
// panicking; callers can still inspect their stable names and schemas.
func NewTools(workspace *Workspace) []Tool {
	return []Tool{
		&workspaceTool{workspace: workspace, name: "ls", description: "List files and directories. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"path": stringProperty("Absolute or workspace-relative directory path")}), invoke: invokeLS},
		&workspaceTool{workspace: workspace, name: "read_file", description: "Read text from a file. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"file_path": stringProperty("Absolute or workspace-relative file path"), "offset": integerProperty("1-based starting line"), "limit": integerProperty("Maximum number of lines; zero means all")}, "file_path"), invoke: invokeRead},
		&workspaceTool{workspace: workspace, name: "write_file", description: "Write text to a file. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"file_path": stringProperty("Absolute or workspace-relative file path"), "content": stringProperty("Complete file content")}, "file_path", "content"), invoke: invokeWrite},
		&workspaceTool{workspace: workspace, name: "edit_file", description: "Replace text in a file. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"file_path": stringProperty("Absolute or workspace-relative file path"), "old_string": stringProperty("Exact text to replace"), "new_string": stringProperty("Replacement text"), "replace_all": booleanProperty("Replace every occurrence")}, "file_path", "old_string", "new_string"), invoke: invokeEdit},
		&workspaceTool{workspace: workspace, name: "glob", description: "Find files matching a glob pattern. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"pattern": stringProperty("Glob pattern"), "path": stringProperty("Absolute or workspace-relative base directory")}, "pattern"), invoke: invokeGlob},
		&workspaceTool{workspace: workspace, name: "grep", description: "Search files with a regular expression. Absolute and workspace-relative paths are accepted.", schema: objectSchema(map[string]any{"pattern": stringProperty("Regular expression"), "path": stringProperty("Absolute or workspace-relative base directory"), "glob": stringProperty("File glob filter"), "file_type": stringProperty("File type filter"), "case_insensitive": booleanProperty("Case-insensitive search"), "enable_multiline": booleanProperty("Enable multiline search"), "after_lines": integerProperty("Lines after each match"), "before_lines": integerProperty("Lines before each match")}, "pattern"), invoke: invokeGrep},
		&workspaceTool{workspace: workspace, name: "execute", description: "Execute a shell command from the workspace root.", schema: objectSchema(map[string]any{"command": stringProperty("Shell command"), "run_in_background": booleanProperty("Run without waiting")}, "command"), invoke: invokeExecute},
	}
}

type workspaceTool struct {
	workspace   *Workspace
	name        string
	description string
	schema      map[string]any
	invoke      func(context.Context, *Workspace, map[string]any) (string, error)
}

func (tool *workspaceTool) Name() string                { return tool.name }
func (tool *workspaceTool) Description() string         { return tool.description }
func (tool *workspaceTool) InputSchema() map[string]any { return cloneJSONMap(tool.schema) }
func (tool *workspaceTool) Invoke(ctx context.Context, arguments map[string]any) (string, error) {
	if tool == nil || tool.workspace == nil {
		return "", fmt.Errorf("workspace tool is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return tool.invoke(ctx, tool.workspace, arguments)
}

func invokeLS(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	path, err := optionalString(arguments, "path")
	if err != nil {
		return "", err
	}
	entries, err := workspace.LsInfo(ctx, &filesystem.LsInfoRequest{Path: path})
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%s", entry.Path, dirMarker(entry.IsDir), entry.Size, entry.ModifiedAt))
	}
	return boundToolText(strings.Join(lines, "\n")), nil
}

func invokeRead(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	path, err := requiredString(arguments, "file_path")
	if err != nil {
		return "", err
	}
	offset, err := optionalInt(arguments, "offset", 0)
	if err != nil {
		return "", err
	}
	limit, err := optionalInt(arguments, "limit", 0)
	if err != nil {
		return "", err
	}
	content, err := workspace.Read(ctx, &filesystem.ReadRequest{FilePath: path, Offset: offset, Limit: limit})
	if err != nil {
		return "", err
	}
	return boundToolText(content.Content), nil
}

func invokeWrite(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	path, err := requiredString(arguments, "file_path")
	if err != nil {
		return "", err
	}
	content, err := requiredStringAllowEmpty(arguments, "content")
	if err != nil {
		return "", err
	}
	if err := workspace.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: content}); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %s", path), nil
}

func invokeEdit(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	path, err := requiredString(arguments, "file_path")
	if err != nil {
		return "", err
	}
	oldString, err := requiredStringAllowEmpty(arguments, "old_string")
	if err != nil {
		return "", err
	}
	if oldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	newString, err := requiredStringAllowEmpty(arguments, "new_string")
	if err != nil {
		return "", err
	}
	replaceAll, err := optionalBool(arguments, "replace_all", false)
	if err != nil {
		return "", err
	}
	if err := workspace.Edit(ctx, &filesystem.EditRequest{FilePath: path, OldString: oldString, NewString: newString, ReplaceAll: replaceAll}); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

func invokeGlob(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	pattern, err := requiredString(arguments, "pattern")
	if err != nil {
		return "", err
	}
	path, err := optionalString(arguments, "path")
	if err != nil {
		return "", err
	}
	entries, err := workspace.GlobInfo(ctx, &filesystem.GlobInfoRequest{Pattern: pattern, Path: path})
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%s", entry.Path, dirMarker(entry.IsDir), entry.Size, entry.ModifiedAt))
	}
	return boundToolText(strings.Join(lines, "\n")), nil
}

func invokeGrep(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	pattern, err := requiredString(arguments, "pattern")
	if err != nil {
		return "", err
	}
	path, err := optionalString(arguments, "path")
	if err != nil {
		return "", err
	}
	glob, err := optionalString(arguments, "glob")
	if err != nil {
		return "", err
	}
	fileType, err := optionalString(arguments, "file_type")
	if err != nil {
		return "", err
	}
	caseInsensitive, err := optionalBool(arguments, "case_insensitive", false)
	if err != nil {
		return "", err
	}
	multiline, err := optionalBool(arguments, "enable_multiline", false)
	if err != nil {
		return "", err
	}
	after, err := optionalInt(arguments, "after_lines", 0)
	if err != nil {
		return "", err
	}
	before, err := optionalInt(arguments, "before_lines", 0)
	if err != nil {
		return "", err
	}
	matches, err := workspace.GrepRaw(ctx, &filesystem.GrepRequest{Pattern: pattern, Path: path, Glob: glob, FileType: fileType, CaseInsensitive: caseInsensitive, EnableMultiline: multiline, AfterLines: after, BeforeLines: before})
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("%s:%d:%s", match.Path, match.Line, match.Content))
	}
	return boundToolText(strings.Join(lines, "\n")), nil
}

func invokeExecute(ctx context.Context, workspace *Workspace, arguments map[string]any) (string, error) {
	command, err := requiredString(arguments, "command")
	if err != nil {
		return "", err
	}
	background, err := optionalBool(arguments, "run_in_background", false)
	if err != nil {
		return "", err
	}
	response, err := workspace.Execute(ctx, &filesystem.ExecuteRequest{Command: command, RunInBackendGround: background})
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", nil
	}
	output := response.Output
	if response.ExitCode != nil {
		output = fmt.Sprintf("exit_code=%d\n%s", *response.ExitCode, output)
	}
	if response.Truncated {
		output += "\n[... output truncated ...]"
	}
	return boundToolText(output), nil
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	result := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		values := make([]any, len(required))
		for index, value := range required {
			values[index] = value
		}
		result["required"] = values
	}
	return result
}
func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func integerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": 0}
}
func booleanProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
func dirMarker(directory bool) string {
	if directory {
		return "dir"
	}
	return "file"
}
func boundToolText(value string) string {
	if len(value) <= workspaceToolOutputLimit {
		return value
	}
	return value[:workspaceToolOutputLimit] + "\n[... output truncated ...]"
}

func requiredString(arguments map[string]any, key string) (string, error) {
	value, err := requiredStringAllowEmpty(arguments, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}
func requiredStringAllowEmpty(arguments map[string]any, key string) (string, error) {
	if arguments == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	value, ok := arguments[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}
func optionalString(arguments map[string]any, key string) (string, error) {
	if arguments == nil || arguments[key] == nil {
		return "", nil
	}
	value, ok := arguments[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return value, nil
}
func optionalBool(arguments map[string]any, key string, fallback bool) (bool, error) {
	if arguments == nil || arguments[key] == nil {
		return fallback, nil
	}
	value, ok := arguments[key].(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}
func optionalInt(arguments map[string]any, key string, fallback int) (int, error) {
	if arguments == nil || arguments[key] == nil {
		return fallback, nil
	}
	switch value := arguments[key].(type) {
	case int:
		return value, nil
	case int64:
		return int(value), nil
	case float64:
		if value != float64(int(value)) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(value), nil
	case string:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}
func cloneJSONMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
