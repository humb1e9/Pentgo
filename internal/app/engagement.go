package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"pentgo/internal/agent"
	"pentgo/internal/config"
	"pentgo/internal/report"
	"pentgo/internal/runtime"
)

// Event 表示 engagement 执行期间可向交互层发送的进度消息。
type Event struct {
	Message string
}

// Request 描述一次自然语言 Agent engagement。
type Request struct {
	Target     runtime.Target
	Intent     string
	OutputRoot string
}

// Result 保存已发布的 artifacts、会话和运行错误。
type Result struct {
	Session   *runtime.AgentSession
	Artifacts report.Artifacts
	RunError  error
}

// Dependencies 提供可替换的时钟、ID 与模型客户端边界。
type Dependencies struct {
	Clock           func() time.Time
	NewEngagementID func(time.Time) (string, error)
	NewAgentClient  func(config.AgentConfig) (agent.Client, error)
}

// Service 组装并运行单一终端 Agent 流程。
type Service struct {
	cfg  config.Config
	deps Dependencies
}

// NewService 使用指定配置和可选依赖创建应用服务。
func NewService(cfg config.Config, dependencies Dependencies) *Service {
	if dependencies.Clock == nil {
		dependencies.Clock = func() time.Time { return time.Now().UTC() }
	}
	if dependencies.NewEngagementID == nil {
		dependencies.NewEngagementID = newEngagementID
	}
	return &Service{cfg: cfg, deps: dependencies}
}

// Run 创建 Runtime 会话、执行模型循环并在所有终态发布 artifacts。
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
	session := runtime.NewSession(request.Target, request.Intent, startedAt)
	session.ID = engagementID
	result := Result{Session: session}

	writer, err := report.NewEngagementWriter(request.OutputRoot, engagementID)
	if err != nil {
		return result, fmt.Errorf("create engagement writer: %w", err)
	}
	defer writer.Abort()
	client, err := service.newAgentClient()
	if err != nil {
		return result, err
	}
	agentConfig := service.cfg.Agent
	targetURL, _ := url.Parse(request.Target.Canonical)
	verificationScope := runtime.NewScope(targetURL.Hostname(), agentConfig.Authorization.AllowedHosts, agentConfig.Authorization.PrivateAllowed())
	verificationClient := &http.Client{Timeout: 15 * time.Second}
	executor := runtime.NewExecutor(runtime.ExecutorConfig{
		WorkDir:             writer.WorkDir(),
		Timeout:             time.Duration(agentConfig.ExecutionTimeoutSeconds) * time.Second,
		MaxParallel:         agentConfig.MaxParallelBlocks,
		MaxOutputBytes:      agentConfig.MaxOutputBytes,
		LineRepeatLimit:     agentConfig.LineRepeatLimit,
		ScanLineRepeatLimit: agentConfig.ScanLineRepeatLimit,
		Evidence:            writer,
	})
	runner := runtime.NewRunner(client, executor, runtime.RunnerConfig{
		MaxTurns:           agentConfig.MaxTurns,
		MaxFindings:        agentConfig.MaxFindings,
		NoCodeLimit:        agentConfig.NoCodeLimit,
		MaxBlocksPerTurn:   agentConfig.MaxBlocksPerTurn,
		ProviderRetryDelay: time.Duration(agentConfig.ProviderRetryDelaySeconds) * time.Second,
		NetworkBackoff:     time.Duration(agentConfig.NetworkBackoffSeconds) * time.Second,
		SoftStuckTurns:     agentConfig.SoftStuckTurns,
		HardStuckTurns:     agentConfig.HardStuckTurns,
		OnEvent: func(event runtime.RunnerEvent) {
			switch event.Kind {
			case "assistant":
				progress(Event{Message: fmt.Sprintf("Assistant turn %d: %s", event.Turn, event.Detail)})
			case "block_started":
				progress(Event{Message: fmt.Sprintf("Block turn %d #%d started (%s)", event.Turn, event.BlockIndex, event.Detail)})
			case "block_finished":
				progress(Event{Message: fmt.Sprintf("Block turn %d #%d finished: %s", event.Turn, event.BlockIndex, event.Detail)})
			}
		},
		Authorizer:        authorizerFromConfig(agentConfig.Authorization),
		AllowedHosts:      agentConfig.Authorization.AllowedHosts,
		AllowPrivateHosts: agentConfig.Authorization.PrivateAllowed(),
		Verifier: runtime.NewHTTPVerifier(
			verificationClient,
			verificationScope,
			agentConfig.VerificationReproductions,
		),
	}, nil, nil)
	progress(Event{Message: "Agent engagement started: " + engagementID})
	result.RunError = runner.Run(ctx, session)
	if result.RunError == nil && ctx.Err() == nil && session.Status == runtime.SessionDone {
		runner.ConsolidateAndVerify(ctx, session)
	}
	reportMarkdown := ""
	if ctx.Err() == nil {
		progress(Event{Message: "Generating final report."})
		validated := runtime.ValidateReportContext(runner.ReportContext(session))
		markdown, reportErr := report.GenerateTerminalMarkdown(ctx, client, validated)
		if reportErr == nil {
			reportMarkdown = markdown
			progress(Event{Message: "Final report generated."})
		} else {
			progress(Event{Message: "Final report fell back to execution timeline."})
		}
	} else {
		progress(Event{Message: "Final report fell back to execution timeline."})
	}
	artifacts, publishErr := writer.PublishWithReport(session, service.now(), reportMarkdown)
	if publishErr != nil {
		return result, fmt.Errorf("publish engagement artifacts: %w", publishErr)
	}
	result.Artifacts = artifacts
	progress(Event{Message: "Agent engagement finished: " + string(session.Status)})
	return result, nil
}

func (service *Service) newAgentClient() (agent.Client, error) {
	if service.deps.NewAgentClient != nil {
		return service.deps.NewAgentClient(service.cfg.Agent)
	}
	configuration := service.cfg.Agent
	providerName := strings.TrimSpace(configuration.Provider)
	var provider config.ModelProviderConfig
	switch providerName {
	case "openai":
		provider = configuration.OpenAI
	case "anthropic":
		provider = configuration.Anthropic
	default:
		return nil, fmt.Errorf("unsupported agent provider: %s", providerName)
	}
	if strings.TrimSpace(provider.Model) == "" {
		return nil, fmt.Errorf("agent %s model is empty", providerName)
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return nil, fmt.Errorf("agent %s base URL is empty", providerName)
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		apiKeyEnv := strings.TrimSpace(provider.APIKeyEnv)
		if apiKeyEnv == "" {
			return nil, fmt.Errorf("agent %s API key environment variable is empty", providerName)
		}
		if value, ok := os.LookupEnv(apiKeyEnv); !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("agent API key is not configured in %s", apiKeyEnv)
		}
	}
	timeout := time.Duration(configuration.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Minute
	}
	providerConfig := agent.ProviderConfig{
		BaseURL:      provider.BaseURL,
		Model:        provider.Model,
		APIKey:       provider.APIKey,
		APIKeyEnv:    provider.APIKeyEnv,
		ThinkingMode: provider.ThinkingMode,
	}
	httpClient := &http.Client{Timeout: timeout}
	switch providerName {
	case "openai":
		return agent.NewOpenAIClient(providerConfig, httpClient, nil), nil
	case "anthropic":
		return agent.NewAnthropicClient(providerConfig, httpClient, nil), nil
	default:
		return nil, fmt.Errorf("unsupported agent provider: %s", providerName)
	}
}

func (service *Service) now() time.Time {
	if service != nil && service.deps.Clock != nil {
		return service.deps.Clock()
	}
	return time.Now().UTC()
}

func newEngagementID(now time.Time) (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "eng-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(random), nil
}

func authorizerFromConfig(auth config.AuthorizationConfig) *runtime.Authorizer {
	if !auth.IsEnabled() {
		return nil
	}
	return runtime.NewAuthorizer(auth.AllowDestructive)
}
