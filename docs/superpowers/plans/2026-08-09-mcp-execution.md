# MCP Execution Architecture

## Goal

Replace PentGo's direct code-execution tools with a first-principles MCP execution contract. The model submits execution jobs, observes explicit states, waits for completion, and can cancel a job. The existing process runner remains an implementation detail behind the execution service.

## Design

1. Add a session-scoped in-process MCP server/client pair. The server exposes `run_execution`, `get_execution`, `wait_execution`, and `cancel_execution`.
2. Add a cancellable execution service with `queued -> running -> completed|failed|cancelled|timed_out` states, execution IDs, bounded waits, and result snapshots. It calls the existing process runner only after language preflight and authorization have completed.
3. Eino receives only the MCP-discovered execution tools. Direct `exec` and `execute_python` handlers and their runner dependency are removed.
4. Keep the transcript, evidence journal, and checkpoint formats. The MCP client records each returned tool result in the existing evidence journal; completed evidence remains the durable audit source while live execution state is session-scoped.
5. Keep the execution client for the full session runtime so later turns can query an existing execution ID. Close it and cancel active jobs when the project/session runtime ends.

## Files

- `internal/runtime/mcp/client.go`: add an in-memory server transport and server lifecycle hook.
- `internal/runtime/mcp/execution_service.go`: add the execution state machine and worker lifecycle.
- `internal/runtime/mcp/execution_server.go`: define the MCP execution contract and handlers.
- `internal/runtime/mcp/execution_server_test.go`: verify discovery, execution states, wait/cancel, preflight, authorization, and evidence flow.
- `internal/runtime/exec/render.go`: move execution-result rendering next to the execution result types.
- `internal/runtime/loop/eino_agent.go`: remove direct execution handlers and accept only discovered MCP execution tools.
- `internal/runtime/loop/eino_run_loop.go`: remove the direct executor dependency from the runner tool set.
- `internal/runtime/loop/runner.go`: remove the direct executor/sleeper contract.
- `internal/coordinator/coordinator.go`: construct one execution MCP client per runner and close it after the run.
- `internal/runtime/loop/prompt.go` and prompt tests: describe the new job-based MCP contract.
- `internal/runtime/loop/eino_run_loop_test.go` and runner tests: exercise execution through the MCP bridge.

## Verification

Run `gofmt`, focused MCP/loop tests, `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build -o /tmp/pentgo-review-build ./cmd/pentgo`, and `git diff --check`.
