package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	sess "pentgo/internal/runtime/session"
)

type Artifacts struct {
	Directory     string
	EvidenceJSONL string
	SessionJSON   string
	Markdown      string
	WorkDirectory string
}

var engagementIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type EngagementWriter struct {
	mu                                 sync.Mutex
	engagementID, finalDir, stagingDir string
	published, aborted                 bool
}

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
	if err := os.Mkdir(filepath.Join(stagingDir, "work"), 0o700); err != nil {
		_ = os.RemoveAll(stagingDir)
		return nil, err
	}
	return &EngagementWriter{engagementID: engagementID, finalDir: finalDir, stagingDir: stagingDir}, nil
}
func (writer *EngagementWriter) WorkDir() string {
	if writer == nil || writer.stagingDir == "" {
		return ""
	}
	return filepath.Join(writer.stagingDir, "work")
}
func (writer *EngagementWriter) EvidencePath() string {
	if writer == nil || writer.stagingDir == "" {
		return ""
	}
	return filepath.Join(writer.stagingDir, "evidence.jsonl")
}

func (writer *EngagementWriter) Publish(session *sess.AgentSession) (Artifacts, error) {
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
	evidencePath := filepath.Join(writer.stagingDir, "evidence.jsonl")
	info, err := os.Stat(evidencePath)
	if err != nil {
		return Artifacts{}, fmt.Errorf("evidence journal: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Artifacts{}, errors.New("evidence journal is not a regular file")
	}
	sessionJSON, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return Artifacts{}, err
	}
	sessionJSON = append(sessionJSON, '\n')
	if err := writeAtomic(filepath.Join(writer.stagingDir, "session.json"), sessionJSON, 0o600); err != nil {
		return Artifacts{}, err
	}
	if err := writeAtomic(filepath.Join(writer.stagingDir, "report.md"), []byte(renderMarkdown(session)), 0o600); err != nil {
		return Artifacts{}, err
	}
	if err := os.Rename(writer.stagingDir, writer.finalDir); err != nil {
		return Artifacts{}, err
	}
	writer.published, writer.stagingDir = true, ""
	return Artifacts{Directory: writer.finalDir, EvidenceJSONL: filepath.Join(writer.finalDir, "evidence.jsonl"), SessionJSON: filepath.Join(writer.finalDir, "session.json"), Markdown: filepath.Join(writer.finalDir, "report.md"), WorkDirectory: filepath.Join(writer.finalDir, "work")}, nil
}
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
func (writer *EngagementWriter) ensureOpen() error {
	if writer.published {
		return errors.New("engagement already published")
	}
	if writer.aborted {
		return errors.New("engagement writer aborted")
	}
	return nil
}
func validateEngagementID(id string) error {
	if !engagementIDPattern.MatchString(id) || id == "." || id == ".." {
		return fmt.Errorf("invalid engagement ID: %q", id)
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
