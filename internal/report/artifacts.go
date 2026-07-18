package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"pentgo/internal/runtime"
)

// Artifacts 表示已发布 engagement 的运行时文件路径。
type Artifacts struct {
	Directory     string
	SessionJSON   string
	Markdown      string
	WorkDirectory string
}

var engagementIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var evidenceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// EngagementWriter 在临时目录中累积运行时证据，并一次性发布完整 engagement。
type EngagementWriter struct {
	mu           sync.Mutex
	engagementID string
	finalDir     string
	stagingDir   string
	published    bool
	aborted      bool
}

// NewEngagementWriter 为指定 engagement 创建 evidence 和 work 子目录。
func NewEngagementWriter(outputRoot, engagementID string) (*EngagementWriter, error) {
	if err := validateEngagementID(engagementID); err != nil {
		return nil, err
	}
	if outputRoot == "" {
		outputRoot = "."
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return nil, err
	}
	finalDir := filepath.Join(outputRoot, engagementID)
	if _, err := os.Lstat(finalDir); err == nil {
		return nil, fmt.Errorf("engagement directory already exists: %s", finalDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	stagingDir, err := os.MkdirTemp(outputRoot, ".pentgo-*")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(stagingDir, 0o700); err != nil {
		_ = os.RemoveAll(stagingDir)
		return nil, err
	}
	for _, name := range []string{"evidence", "work"} {
		if err := os.Mkdir(filepath.Join(stagingDir, name), 0o700); err != nil {
			_ = os.RemoveAll(stagingDir)
			return nil, err
		}
	}
	return &EngagementWriter{engagementID: engagementID, finalDir: finalDir, stagingDir: stagingDir}, nil
}

// WorkDir 返回本次 engagement 的跨轮代码工作目录。
func (writer *EngagementWriter) WorkDir() string {
	if writer == nil || writer.stagingDir == "" {
		return ""
	}
	return filepath.Join(writer.stagingDir, "work")
}

// WriteEvidence 将完整的单块执行证据写入独立 JSON 文件。
func (writer *EngagementWriter) WriteEvidence(name string, value any) (string, error) {
	if writer == nil {
		return "", errors.New("nil engagement writer")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.ensureOpen(); err != nil {
		return "", err
	}
	if err := validateEvidenceName(name); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	relativePath := filepath.Join("evidence", name+".json")
	path := filepath.Join(writer.stagingDir, relativePath)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("evidence already exists: %s", relativePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := writeAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return relativePath, nil
}

// Publish 写入 session.json 和 report.md，并原子发布已有 work 与 evidence 文件。
func (writer *EngagementWriter) Publish(session *runtime.AgentSession, generatedAt time.Time) (Artifacts, error) {
	return writer.PublishWithReport(session, generatedAt, "")
}

// PublishWithReport 写入模型生成的 Markdown；空文本时使用确定性时间线回退。
func (writer *EngagementWriter) PublishWithReport(session *runtime.AgentSession, generatedAt time.Time, markdown string) (Artifacts, error) {
	if writer == nil {
		return Artifacts{}, errors.New("nil engagement writer")
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.ensureOpen(); err != nil {
		return Artifacts{}, err
	}
	if session == nil {
		return Artifacts{}, errors.New("nil session")
	}
	if session.ID != writer.engagementID {
		return Artifacts{}, fmt.Errorf("session engagement ID %q does not match writer %q", session.ID, writer.engagementID)
	}
	sessionJSON, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return Artifacts{}, err
	}
	sessionJSON = append(sessionJSON, '\n')
	if err := writeAtomic(filepath.Join(writer.stagingDir, "session.json"), sessionJSON, 0o600); err != nil {
		return Artifacts{}, err
	}
	markdown = renderPublishedMarkdown(session, generatedAt, markdown)
	if err := writeAtomic(filepath.Join(writer.stagingDir, "report.md"), []byte(markdown), 0o600); err != nil {
		return Artifacts{}, err
	}
	if err := os.Rename(writer.stagingDir, writer.finalDir); err != nil {
		return Artifacts{}, err
	}
	writer.published = true
	writer.stagingDir = ""
	return Artifacts{
		Directory:     writer.finalDir,
		SessionJSON:   filepath.Join(writer.finalDir, "session.json"),
		Markdown:      filepath.Join(writer.finalDir, "report.md"),
		WorkDirectory: filepath.Join(writer.finalDir, "work"),
	}, nil
}

func renderPublishedMarkdown(session *runtime.AgentSession, generatedAt time.Time, narrative string) string {
	if strings.TrimSpace(narrative) == "" {
		return renderMarkdown(session, generatedAt)
	}
	var builder strings.Builder
	builder.WriteString("# PentGo Agent Report\n\n")
	builder.WriteString(RenderVerifiedFindings(session.Findings))
	builder.WriteString("\n")
	builder.WriteString(strings.TrimSpace(narrative))
	builder.WriteString("\n")
	return builder.String()
}

// Abort 删除尚未发布的临时 engagement 目录。
func (writer *EngagementWriter) Abort() {
	if writer == nil {
		return
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.published || writer.aborted {
		return
	}
	writer.aborted = true
	if writer.stagingDir != "" {
		_ = os.RemoveAll(writer.stagingDir)
		writer.stagingDir = ""
	}
}

// WriteArtifacts 创建 writer 并发布终态 Runtime 会话。
func WriteArtifacts(session *runtime.AgentSession, outputRoot string, generatedAt time.Time) (Artifacts, error) {
	if session == nil {
		return Artifacts{}, errors.New("nil session")
	}
	writer, err := NewEngagementWriter(outputRoot, session.ID)
	if err != nil {
		return Artifacts{}, err
	}
	defer writer.Abort()
	return writer.Publish(session, generatedAt)
}

func (writer *EngagementWriter) ensureOpen() error {
	if writer.published {
		return errors.New("engagement already published")
	}
	if writer.aborted {
		return errors.New("engagement writer aborted")
	}
	return nil
}

func validateEngagementID(engagementID string) error {
	if !engagementIDPattern.MatchString(engagementID) || engagementID == "." || engagementID == ".." {
		return fmt.Errorf("invalid engagement ID: %q", engagementID)
	}
	return nil
}

func validateEvidenceName(name string) error {
	if !evidenceNamePattern.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid evidence name: %q", name)
	}
	return nil
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pentgo-file-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
