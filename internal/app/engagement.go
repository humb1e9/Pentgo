package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/report"
	"pentgo/internal/runtime/authz"
	"pentgo/internal/runtime/evidence"
	"pentgo/internal/runtime/exec"
	"pentgo/internal/runtime/loop"
	runtimeMCP "pentgo/internal/runtime/mcp"
	sess "pentgo/internal/runtime/session"

	"github.com/cloudwego/eino/components/model"
)

type Event struct{ Message string }
type Request struct {
	Target     sess.Target
	Intent     string
	OutputRoot string
}
type Result struct {
	Session   *sess.AgentSession
	Artifacts report.Artifacts
	RunError  error
}
type Dependencies struct {
	Clock           func() time.Time
	NewEngagementID func(time.Time) (string, error)
	NewEinoModel    func(context.Context, config.AgentConfig) (model.ToolCallingChatModel, error)
}
type Service struct {
	cfg  config.Config
	deps Dependencies
}

func NewService(cfg config.Config, dependencies Dependencies) *Service {
	if dependencies.Clock == nil {
		dependencies.Clock = func() time.Time { return time.Now().UTC() }
	}
	if dependencies.NewEngagementID == nil {
		dependencies.NewEngagementID = newEngagementID
	}
	return &Service{cfg: cfg, deps: dependencies}
}

func (service *Service) Run(ctx context.Context, request Request, progress func(Event)) (Result, error) {
	if service == nil {
		return Result{}, errors.New("nil engagement service")
	}
	if strings.TrimSpace(request.Target.Canonical) == "" || strings.TrimSpace(request.Intent) == "" {
		return Result{}, errors.New("invalid runtime task")
	}
	if progress == nil {
		progress = func(Event) {}
	}
	startedAt := service.now()
	engagementID, err := service.deps.NewEngagementID(startedAt)
	if err != nil {
		return Result{}, fmt.Errorf("create engagement ID: %w", err)
	}
	session := sess.NewSession(request.Target, request.Intent, startedAt)
	session.ID = engagementID
	result := Result{Session: session}
	writer, err := report.NewEngagementWriter(request.OutputRoot, engagementID)
	if err != nil {
		return result, fmt.Errorf("create engagement writer: %w", err)
	}
	defer writer.Abort()
	agentConfig := service.cfg.Agent
	journal, err := evidence.NewJournal(writer.EvidencePath(), mcpSecrets(agentConfig.MCP)...)
	if err != nil {
		return result, fmt.Errorf("create evidence journal: %w", err)
	}
	executor := exec.NewExecutor(exec.ExecutorConfig{WorkDir: writer.WorkDir(), Timeout: time.Duration(agentConfig.ExecutionTimeoutSeconds) * time.Second, MaxOutputBytes: agentConfig.MaxOutputBytes, LineRepeatLimit: agentConfig.LineRepeatLimit, ScanLineRepeatLimit: agentConfig.ScanLineRepeatLimit})
	if err := session.Start(startedAt); err != nil {
		_ = journal.Close()
		return result, err
	}
	var mcpClient *runtimeMCP.Client
	if agentConfig.MCP != nil {
		connectCtx, cancel := context.WithTimeout(ctx, time.Duration(agentConfig.RequestTimeoutSeconds)*time.Second)
		mcpClient, err = runtimeMCP.ConnectStdio(connectCtx, *agentConfig.MCP, journal, agentConfig.MaxOutputBytes)
		cancel()
		if err != nil {
			result.RunError = err
			_ = session.Fail("mcp_init_error", service.now())
			return service.finishEngagement(result, writer, journal, executor, nil, progress)
		}
	}
	runnerConfig := loop.RunnerConfig{MaxTurns: agentConfig.MaxTurns, NetworkBackoff: time.Duration(agentConfig.NetworkBackoffSeconds) * time.Second, OnEvent: func(event loop.RunnerEvent) {
		switch event.Kind {
		case "assistant":
			progress(Event{Message: fmt.Sprintf("Assistant turn %d: %s", event.Turn, event.Detail)})
		case "block_started":
			progress(Event{Message: fmt.Sprintf("Block turn %d #%d started (%s)", event.Turn, event.BlockIndex, event.Detail)})
		case "block_finished":
			progress(Event{Message: fmt.Sprintf("Block turn %d #%d finished: %s", event.Turn, event.BlockIndex, event.Detail)})
		}
	}, Authorizer: authorizerFromConfig(agentConfig.Authorization), AllowedHosts: agentConfig.Authorization.AllowedHosts, AllowPrivateHosts: agentConfig.Authorization.PrivateAllowed()}
	if mcpClient != nil {
		runnerConfig.MCPTools = mcpClient.Tools()
	}
	runner := loop.NewRunner(executor, journal, runnerConfig, nil, nil)
	progress(Event{Message: "Agent engagement started: " + engagementID})
	chatModel, modelErr := service.newEinoModel(ctx)
	if modelErr != nil {
		result.RunError = modelErr
		_ = session.Fail("model_init_error", service.now())
	} else {
		result.RunError = runner.RunEino(ctx, session, chatModel)
	}
	return service.finishEngagement(result, writer, journal, executor, mcpClient, progress)
}

func (service *Service) finishEngagement(result Result, writer *report.EngagementWriter, journal *evidence.Journal, executor *exec.Executor, mcpClient *runtimeMCP.Client, progress func(Event)) (Result, error) {
	if mcpClient != nil {
		if err := mcpClient.Close(); err != nil && result.RunError == nil {
			result.RunError = fmt.Errorf("close MCP client: %w", err)
			if result.Session.Status == sess.SessionRunning {
				_ = result.Session.Fail("mcp_close_error", service.now())
			}
		}
	}
	if err := journal.Close(); err != nil && result.RunError == nil {
		result.RunError = err
		if result.Session.Status == sess.SessionRunning {
			_ = result.Session.Fail("evidence_error", service.now())
		}
	}
	if err := executor.CleanupGeneratedScripts(); err != nil {
		return result, fmt.Errorf("cleanup Runtime scripts: %w", err)
	}
	artifacts, err := writer.Publish(result.Session)
	if err != nil {
		return result, fmt.Errorf("publish engagement artifacts: %w", err)
	}
	result.Artifacts = artifacts
	progress(Event{Message: "Agent engagement finished: " + string(result.Session.Status)})
	return result, nil
}

func mcpSecrets(configuration *config.MCPConfig) []string {
	if configuration == nil {
		return nil
	}
	secrets := make([]string, 0, len(configuration.Env))
	for _, value := range configuration.Env {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

func (service *Service) newEinoModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	if service.deps.NewEinoModel != nil {
		return service.deps.NewEinoModel(ctx, service.cfg.Agent)
	}
	providerName := strings.TrimSpace(service.cfg.Agent.Provider)
	var provider config.ModelProviderConfig
	switch providerName {
	case "openai":
		provider = service.cfg.Agent.OpenAI
	case "anthropic":
		provider = service.cfg.Agent.Anthropic
	default:
		return nil, fmt.Errorf("unsupported agent provider: %s", providerName)
	}
	if strings.TrimSpace(provider.Model) == "" {
		return nil, fmt.Errorf("agent %s model is empty", providerName)
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return nil, fmt.Errorf("agent %s base URL is empty", providerName)
	}
	timeout := time.Duration(service.cfg.Agent.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Minute
	}
	providerConfig := agent.ProviderConfig{BaseURL: provider.BaseURL, Model: provider.Model, APIKey: provider.APIKey, APIKeyEnv: provider.APIKeyEnv, ThinkingMode: provider.ThinkingMode}
	client := &http.Client{Timeout: timeout}
	if providerName == "anthropic" {
		return agent.NewEinoAnthropicModel(ctx, providerConfig, client, nil)
	}
	return agent.NewEinoOpenAIModel(ctx, providerConfig, client, nil)
}
func (service *Service) now() time.Time {
	if service != nil && service.deps.Clock != nil {
		return service.deps.Clock()
	}
	return time.Now().UTC()
}
func newEngagementID(now time.Time) (string, error) {
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "eng-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(value), nil
}
func authorizerFromConfig(auth config.AuthorizationConfig) *authz.Authorizer {
	if !auth.IsEnabled() {
		return nil
	}
	return authz.NewAuthorizer(auth.AllowDestructive)
}
