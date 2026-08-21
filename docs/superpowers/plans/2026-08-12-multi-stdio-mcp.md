# Multi-Transport MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one PentGo project connect to multiple named stdio, Streamable HTTP, and SSE MCP servers and expose all discovered tools without name collisions.

**Architecture:** Use a name-to-server map for `agent.mcp`, while loading the former single-server object as `default`. The MCP adapter selects the configured transport, exposes each remote tool under its original globally unique name, and returns one `ToolProvider`/`ToolCloser` aggregate to `ProjectRuntime`.

**Tech Stack:** Go 1.25, official MCP Go SDK, existing Eino tool adapter, SQLite evidence store.

## Global Constraints

- Support stdio, Streamable HTTP, and legacy SSE MCP transports.
- Require every discovered external tool name to be globally unique across configured servers.
- Reject invalid server names and duplicate tool names before exposing them to the model.
- Close all opened MCP clients if a later connection fails.

---

### Task 1: Model Multiple Server Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `AgentConfig.MCP map[string]MCPConfig`.

- [ ] Replace `MCP *MCPConfig` with `MCP map[string]MCPConfig` using JSON key `mcp`.
- [ ] Load a configuration containing named `nmap` and `browser` server entries and assert both command, args, and environment values survive decoding.
- [ ] Run `go test ./internal/config -count=1`.

### Task 2: Aggregate MCP Clients and Namespace Tools

**Files:**
- Modify: `internal/adapters/mcp/client.go`
- Modify: `internal/adapters/mcp/client_test.go`

**Interfaces:**
- Produces: `ConnectAll(context.Context, config.MCPServers, *storage.EvidenceStore, int, string, string) (*Clients, error)`.
- Produces: `Clients` implementing `agent.ToolProvider` and `agent.ToolCloser`.

- [ ] Add a two-server fixture test asserting globally unique tool names are exposed unchanged and both calls return their server result.
- [ ] Select `stdio`, `http`/`streamable-http`, or `sse` per config, connect servers in sorted name order, and close every previously opened client on error.
- [ ] Reject duplicate tool names across servers while keeping each globally unique Server tool name unchanged.
- [ ] Run `go test ./internal/adapters/mcp -count=1`.

### Task 3: Wire Project Lifecycle and Document Configuration

**Files:**
- Modify: `internal/app/coordinator.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Consumes: `mcp.ConnectAllStdio` aggregate provider.

- [ ] Replace the single client setup with aggregate client setup and pass all configured environment values to evidence redaction.
- [ ] Document the `agent.mcp` named-server configuration shape and namespaced tool names.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, `go build ./cmd/...`, `go mod verify`, and `git diff --check`.
