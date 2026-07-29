package loop

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/evidence"
	"pentgo/internal/runtime/exec"
	"pentgo/skills"

	"github.com/cloudwego/eino/components/tool"
)

type BlockExecutor interface {
	Execute(context.Context, exec.ExecutionInput) []exec.ExecutionResult
}
type SkillLoader func(string) (string, error)
type Sleeper func(context.Context, time.Duration) error

type RunnerConfig struct {
	MaxTurns          int
	NetworkBackoff    time.Duration
	OnEvent           func(RunnerEvent)
	SkillCatalog      []skills.Skill
	Authorizer        *authz.Authorizer
	AllowedHosts      []string
	AllowPrivateHosts bool
	MCPTools          []tool.BaseTool
}

type RunnerEvent struct {
	Turn       int
	Kind       string
	BlockIndex int
	Detail     string
}

type Runner struct {
	executor BlockExecutor
	journal  *evidence.Journal
	config   RunnerConfig
	load     SkillLoader
	sleep    Sleeper
	catalog  []skills.Skill
}

func NewRunner(executor BlockExecutor, journal *evidence.Journal, config RunnerConfig, load SkillLoader, sleep Sleeper) *Runner {
	if config.NetworkBackoff <= 0 {
		config.NetworkBackoff = 15 * time.Second
	}
	if load == nil {
		load = skills.Load
	}
	if sleep == nil {
		sleep = sleepContext
	}
	catalog := config.SkillCatalog
	if catalog == nil {
		catalog = skills.Catalog()
	}
	return &Runner{executor: executor, journal: journal, config: config, load: load, sleep: sleep, catalog: catalog}
}

func RenderExecutionResults(turn int, results []exec.ExecutionResult) string {
	var builder strings.Builder
	for _, result := range results {
		fmt.Fprintf(&builder, "=== PENTGO EXECUTION RESULT ===\nturn: %d\nlanguage: %s\nstatus: %s\nexit_code: %d\nstdout:\n%sstderr:\n%s", turn, result.Block.Language, result.Status, result.ExitCode, ensureTrailingNewline(result.Stdout), ensureTrailingNewline(result.Stderr))
		if result.Error != "" {
			fmt.Fprintf(&builder, "error:\n%s\n", result.Error)
		}
		builder.WriteString("=== END PENTGO EXECUTION RESULT ===")
	}
	return builder.String()
}

func ensureTrailingNewline(value string) string {
	if value == "" {
		return "\n"
	}
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func hasNetworkFriction(results []exec.ExecutionResult) bool {
	for _, result := range results {
		value := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + result.Error)
		for _, marker := range []string{"429", "403", "503", "connection reset", "timeout", "timed out"} {
			if strings.Contains(value, marker) {
				return true
			}
		}
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (runner *Runner) emit(event RunnerEvent) {
	if runner != nil && runner.config.OnEvent != nil {
		runner.config.OnEvent(event)
	}
}

func assistantSummary(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 160 {
				return line[:160]
			}
			return line
		}
	}
	return "response received"
}

func hostOf(canonical string) string {
	parsed, err := url.Parse(canonical)
	if err != nil {
		return canonical
	}
	return parsed.Hostname()
}
