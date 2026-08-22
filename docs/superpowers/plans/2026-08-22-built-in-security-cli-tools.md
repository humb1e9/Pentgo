# Config-Driven Local CLI Tools Implementation Plan

> Implemented architecture: `mcp.LocalRegistry` exposes ordinary local CLI programs declared in `agent.local_tools`; `internal/adapters/mcp/client.go` remains the client for user-configured external MCP services.

## Scope

- `agent.local_tools` is a JSON map from a model-visible name to a direct CLI `command` and optional `description`.
- There is no fixed tool list: users may configure `amass`, `subfinder`, `gau`, `paramspider`, `katana`, `httpx`, `wafw00f`, or any other local CLI.
- PentGo does not download, resolve, identity-check, version-check, or otherwise validate configured commands. Users own that choice.
- Every local tool accepts `{"args": ["..."]}` and invokes the configured command through `exec.CommandContext`, never a shell.
- LocalRegistry tools and configured external MCP tools are combined at project open, with duplicate names rejected before session restoration.
- Existing `execute` remains unchanged; no `security-tools-mcp` process is introduced.

## Implemented Files

### Configuration and LocalRegistry

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/adapters/mcp/local_registry.go`
- `internal/adapters/mcp/local_registry_test.go`

The registry sorts configured tool names for deterministic exposure, preserves custom descriptions, supplies a neutral default description, validates the uniform `args` array, captures bounded combined stdout/stderr, and uses context cancellation. A configured but unavailable command fails only when invoked.

### Provider aggregation and runtime wiring

- `internal/app/tool_provider.go`
- `internal/app/tool_provider_test.go`
- `internal/app/coordinator.go`
- `internal/app/coordinator_test.go`
- `internal/app/tools.go`

Coordinator builds LocalRegistry from `Agent.LocalTools`, combines it with external MCP providers, and retains the existing evidence decorator for all project-level tools.

### Documentation

- `README.md`
- `docs/ARCHITECTURE.md`
- `docs/TECHNICAL.md`

## Verification

```bash
gofmt -w internal/config/config.go internal/config/config_test.go internal/adapters/mcp/local_registry.go internal/adapters/mcp/local_registry_test.go internal/app/coordinator.go internal/app/coordinator_test.go internal/app/tool_provider.go internal/app/tool_provider_test.go internal/app/tools.go internal/cli/model.go
go test ./... -count=1
go test ./... -race -count=1
go build ./...
go vet ./...
git diff --check
```
