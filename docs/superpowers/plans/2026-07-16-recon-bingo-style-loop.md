# Recon Bingo-Style Agent Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Bingo-style per-engagement conversation transcript that feeds each registered Recon action result back to the model as text and uses model markers plus Bingo-like stop conditions to finish the loop.

**Architecture:** Replace the decision-only provider boundary with a response containing optional assistant text and an optional single structured tool decision. The Recon runner owns a bounded transcript, records every selected action as an assistant message, renders each persisted `CallOutcome` as a user message, and sends the transcript on the next provider request. Completion moves from the `recon_complete` tool to text markers and loop-state conditions while preserving registered actions, evidence artifacts, cancellation, and the configured hard turn ceiling.

**Tech Stack:** Go 1.25, standard library `net/http` and `encoding/json`, existing Recon state/evidence model, Go test.

## Global Constraints

- Keep the existing registered action catalog, target validation, action argument validation, evidence writer, and State/Decision/Batch/Call lifecycle.
- Do not execute model-generated Python, Bash, arbitrary commands, executable paths, or target arguments.
- Do not persist raw model responses or the transient transcript in session/evidence artifacts.
- Preserve `max_turns` as a terminal hard ceiling and retain existing cancellation, invalid-action, invalid-argument, execution-infrastructure, and evidence-write failures.
- Leave `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md` untouched because it contains user changes.

---

### Task 1: Replace the OpenAI Decision Contract with a Conversation Response Contract

**Files:**
- Modify: `internal/agent/types.go:14-46`
- Modify: `internal/agent/openai.go:11-170`
- Modify: `tests/_packages/internal/agent/client_test.go:13-142`

**Interfaces:**
- Consumes: `agent.ToolDefinition` and existing provider configuration.
- Produces: `agent.Client.Respond(context.Context, Request) (Response, error)`, `agent.Message`, and `agent.Response` for both provider clients and the Recon runner.

- [ ] **Step 1: Write failing OpenAI response-contract tests**

Replace the first OpenAI test with a request containing ordered transcript messages and assert the serialized request uses `tool_choice: "auto"`, `parallel_tool_calls: false`, and the exact system/user/assistant/user sequence. Return content plus one tool call and assert both fields survive parsing.

```go
func TestOpenAIClientRespondsWithTranscriptAndToolCall(t *testing.T) {
    // Server asserts body["tool_choice"] == "auto" and decodes body["messages"].
    // It returns content plus a single dns function call.
    response, err := client.Respond(context.Background(), Request{
        SystemPrompt: "system",
        Messages: []Message{
            {Role: "user", Content: "initial"},
            {Role: "assistant", Content: "ACTION_SELECTED\ntool: http_metadata"},
            {Role: "user", Content: "=== PENTGO TOOL EXECUTION RESULT ==="},
        },
        Tools: []ToolDefinition{dnsToolDefinition()},
    })
    if err != nil || response.Content != "I will resolve the host." || response.Decision == nil || response.Decision.ToolID != "dns" {
        t.Fatalf("response/err = %+v/%v", response, err)
    }
}
```

Add `TestOpenAIClientRespondsWithCompletionText`, whose fixture returns `{"choices":[{"message":{"content":"TASK_COMPLETE\\nbaseline complete"}}]}` and asserts a nil decision. Keep the existing configured API-key and configured thinking tests, but call `Respond` and assert their tool decision through `response.Decision`. Keep the multiple-tool-call rejection test and make it call `Respond`.

- [ ] **Step 2: Run the focused test and verify it fails for the missing response API**

Run:

```bash
go test ./internal/agent -run 'TestOpenAIClientRespondsWithTranscriptAndToolCall|TestOpenAIClientRespondsWithCompletionText' -count=1
```

Expected: FAIL because `Client.Respond`, `Request.Messages`, and `Response` do not exist yet.

- [ ] **Step 3: Define the shared response and message types**

In `internal/agent/types.go`, replace the decision-only interface and request shape with these declarations:

```go
type Client interface {
    Respond(context.Context, Request) (Response, error)
}

type Request struct {
    SystemPrompt string
    Messages     []Message
    Tools        []ToolDefinition
}

type Message struct {
    Role    string
    Content string
}

type Response struct {
    Content  string
    Decision *Decision
}
```

Keep `Decision` as the validated provider-neutral tool name and argument map. Do not add provider-native tool-call IDs or tool-result role messages; the runner transcript is deliberately plain assistant/user text.

- [ ] **Step 4: Implement OpenAI transcript serialization and optional-tool parsing**

Rename `Decide` to `Respond`. Build `openAIRequest.Messages` from the system prompt followed by every `request.Messages` item, retain the optional `thinking` field, set `ToolChoice` to `"auto"`, and retain `ParallelToolCalls: false`.

Replace `parseOpenAIDecision` with `parseOpenAIResponse`. Decode `choices[0].message.content` and `tool_calls`. Return an empty `Response` when content and tool calls are both absent, return a content-only response when there are zero calls, return a `Response{Content: content, Decision: &decision}` for exactly one valid function call, and return the existing descriptive error for more than one call or malformed arguments.

```go
func parseOpenAIResponse(data []byte) (Response, error) {
    // Decode message.Content and message.ToolCalls.
    // len(calls) == 0 returns Response{Content: strings.TrimSpace(message.Content)}.
    // len(calls) > 1 returns the existing "exactly one tool call" error.
    // len(calls) == 1 parses arguments and returns a non-nil Decision pointer.
}
```

- [ ] **Step 5: Run the complete agent client package**

Run:

```bash
go test ./internal/agent -count=1
```

Expected: PASS, including API-key, configured-thinking, HTTP-error, malformed-tool-input, and multiple-tool-call cases.

- [ ] **Step 6: Commit the response-contract and OpenAI work**

```bash
git add internal/agent/types.go internal/agent/openai.go tests/_packages/internal/agent/client_test.go
git commit -m "feat: add conversational agent responses"
```

### Task 2: Adapt Anthropic and the Application Test to the Response Contract

**Files:**
- Modify: `internal/agent/anthropic.go:11-161`
- Modify: `tests/_packages/internal/agent/client_test.go:80-142`
- Modify: `tests/_packages/internal/app/engagement_test.go:111-143`

**Interfaces:**
- Consumes: `agent.Request.Messages` and `agent.Response` from Task 1.
- Produces: Anthropic text-or-tool responses with the same contract as OpenAI.

- [ ] **Step 1: Write failing Anthropic text and transcript tests**

Add a test that sends one user, one assistant, and one user transcript message and asserts the Anthropic JSON contains those messages in that order and no forced `tool_choice` field. Return a `text` block followed by a `tool_use` block and assert both `Response.Content` and `Response.Decision`. Add a second test whose fixture contains only `{"type":"text","text":"MISSION_COMPLETE\\nfinished"}` and asserts a nil decision.

```go
func TestAnthropicClientRespondsWithTextAndToolUse(t *testing.T) {
    response, err := client.Respond(context.Background(), Request{
        SystemPrompt: "system",
        Messages: []Message{{Role: "user", Content: "initial"}},
        Tools: []ToolDefinition{dnsToolDefinition()},
    })
    if err != nil || response.Content != "Resolving now." || response.Decision == nil || response.Decision.ToolID != "dns" {
        t.Fatalf("response/err = %+v/%v", response, err)
    }
}
```

- [ ] **Step 2: Run the Anthropic-focused tests and verify they fail**

Run:

```bash
go test ./internal/agent -run 'TestAnthropicClientRespondsWithTextAndToolUse|TestAnthropicClientRespondsWithCompletionText' -count=1
```

Expected: FAIL because `AnthropicClient` still implements `Decide` and forces a tool-use result.

- [ ] **Step 3: Serialize the transcript and parse text blocks in AnthropicClient**

Rename `Decide` to `Respond`. Convert `request.Messages` into `[]anthropicMessage` without adding a duplicate user prompt. Remove `ToolChoice` from `anthropicRequest` and from its payload construction so Anthropic may return normal content or one `tool_use` block.

Replace `parseAnthropicDecision` with `parseAnthropicResponse`: concatenate `text` block values in order, collect only `tool_use` blocks, return content-only for zero tool-use blocks, return content plus one parsed decision for exactly one block, and retain errors for more than one tool-use block or malformed input.

```go
func parseAnthropicResponse(data []byte) (Response, error) {
    // Join type == "text" blocks with "\n".
    // Permit zero or one type == "tool_use" block.
    // Parse a single block with parseAnthropicToolInput.
}
```

- [ ] **Step 4: Update the application-level thinking pass-through test**

In `TestServicePassesOpenAIThinkingModeToClient`, return a `dns` tool call instead of `recon_complete`, call `client.Respond`, and assert `response.Decision.ToolID == "dns"`. This keeps coverage that `newAgentClient` forwards `thinking_mode: "disabled"` without depending on the retired completion tool.

- [ ] **Step 5: Run application and agent tests**

Run:

```bash
go test ./internal/agent ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the Anthropic and application migration**

```bash
git add internal/agent/anthropic.go tests/_packages/internal/agent/client_test.go tests/_packages/internal/app/engagement_test.go
git commit -m "feat: support text agent responses across providers"
```

### Task 3: Add a Bounded Recon Transcript and Tool-Result Text Renderer

**Files:**
- Create: `internal/redteam/phases/recon/agent_transcript.go`
- Create: `tests/packages/internal/redteam/phases/recon/agent_transcript.go` (symbolic link to `../../../../../../internal/redteam/phases/recon/agent_transcript.go`)
- Create: `tests/packages/internal/redteam/phases/recon/agent_transcript_test.go`

**Interfaces:**
- Consumes: `AgentInput`, `agent.Decision`, `PlannedCall`, `Call`, and `CallOutcome`.
- Produces: an in-memory transcript with `Messages() []agent.Message`, execution-result text, loop prompts, and response fingerprints for `AgentRunner`.

- [ ] **Step 1: Write failing transcript tests**

Create tests for all transcript invariants:

```go
func TestAgentTranscriptRetainsInitialMessageAndLastSixteenLaterMessages(t *testing.T) {
    transcript := newAgentTranscript(AgentInput{Target: "https://example.test", Intent: "资产侦察"})
    for i := 0; i < 20; i++ {
        transcript.Append(agent.Message{Role: "assistant", Content: fmt.Sprintf("message-%d", i)})
    }
    messages := transcript.Messages()
    if len(messages) != 17 || !strings.Contains(messages[0].Content, "Canonical target: https://example.test") || strings.Contains(messages[1].Content, "message-0") {
        t.Fatalf("messages = %#v", messages)
    }
}

func TestRenderAgentExecutionResultIncludesBoundedOutcome(t *testing.T) {
    text := renderAgentExecutionResult(Call{Tool: "dns"}, CallOutcome{Status: CallStatusFailed, Summary: "resolver error", Error: "timeout", EvidencePath: "evidence/002-dns.json", Observations: []Observation{{Kind: "dns", Summary: "lookup failed"}}})
    for _, want := range []string{"PENTGO TOOL EXECUTION RESULT", "tool: dns", "status: failed", "resolver error", "timeout", "evidence/002-dns.json", "END PENTGO TOOL EXECUTION RESULT"} {
        if !strings.Contains(text, want) { t.Fatalf("result = %q", text) }
    }
}
```

Add a test with six observations and a 3,100-byte summary asserting five observations and a bounded 3,000-byte output. Add fingerprint tests that equal trimmed content plus the same tool ID match, while a changed tool ID does not.

- [ ] **Step 2: Run the transcript tests and verify they fail**

Run:

```bash
go test ./tests/packages/internal/redteam/phases/recon -run 'TestAgentTranscript|TestRenderAgentExecutionResult|TestAgentResponseFingerprint' -count=1
```

Expected: FAIL because `agent_transcript.go` and its helpers do not exist.

- [ ] **Step 3: Implement transcript ownership and bounded rendering**

Define these constants and helpers in `agent_transcript.go`:

```go
const (
    maxAgentTranscriptLaterMessages = 16
    maxAgentTranscriptMessageBytes  = 3000
    maxAgentTranscriptObservations  = 5
)

type agentTranscript struct {
    initial agent.Message
    later   []agent.Message
}

func newAgentTranscript(input AgentInput) *agentTranscript
func (t *agentTranscript) Append(message agent.Message)
func (t *agentTranscript) Messages() []agent.Message
func renderAgentActionSelected(actionID, reason string) string
func renderAgentExecutionResult(call Call, outcome CallOutcome) string
func renderAgentContinueRequired() string
func renderAgentStrategyChange() string
func agentResponseFingerprint(response agent.Response) string
```

`newAgentTranscript` builds one initial `user` message from the canonical target and optional intent. `Append` trims content to 3,000 bytes at UTF-8 rune boundaries, ignores empty messages, and retains the initial message plus the last 16 later entries. Implement a shared `boundAgentTranscriptText` helper that preserves valid UTF-8 while applying that byte cap. The action renderer emits `ACTION_SELECTED`, `tool`, and `reason`. The result renderer uses the exact header and tail from the approved design, includes `tool`, `status`, `summary`, optional `error`, at most five observation `kind: summary` lines, optional `evidence`, and the fixed `NEXT ACTION` instruction. It applies the same UTF-8-safe cap after constructing the full message.

- [ ] **Step 4: Run the transcript package tests**

Run:

```bash
go test ./tests/packages/internal/redteam/phases/recon -run 'TestAgentTranscript|TestRenderAgentExecutionResult|TestAgentResponseFingerprint' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit transcript support**

```bash
git add internal/redteam/phases/recon/agent_transcript.go tests/packages/internal/redteam/phases/recon/agent_transcript.go tests/packages/internal/redteam/phases/recon/agent_transcript_test.go
git commit -m "feat: add bounded recon agent transcript"
```

### Task 4: Replace Recon Completion with the Bingo-Style Transcript Loop

**Files:**
- Modify: `internal/redteam/phases/recon/agent_runner.go:14-340`
- Modify: `tests/packages/internal/redteam/phases/recon/agent_runner_test.go:13-148`

**Interfaces:**
- Consumes: `agent.Client.Respond`, `agentTranscript`, `AgentCatalog`, and existing State/Evidence APIs.
- Produces: marker-, no-action-, and stuck-aware Recon completion with a per-turn assistant-decision/result transcript.

- [ ] **Step 1: Rewrite the runner test fixture and add failing loop tests**

Replace `scriptedAgentClient.decisions` and `Decide` with `responses []agent.Response` and `Respond`, preserving request capture. Replace completion-tool fixtures with marker content.

```go
func (c *scriptedAgentClient) Respond(_ context.Context, request agent.Request) (agent.Response, error) {
    c.requests = append(c.requests, request)
    if c.calls >= len(c.responses) { return agent.Response{}, errors.New("unexpected model call") }
    response := c.responses[c.calls]
    c.calls++
    return response, nil
}

func TestAgentRunnerFeedsAssistantDecisionAndResultToNextTurn(t *testing.T) {
    client := &scriptedAgentClient{responses: []agent.Response{
        {Decision: &agent.Decision{ToolID: "dns", Arguments: map[string]any{"reason": "resolve"}}},
        {Content: "TASK_COMPLETE\nbaseline complete"},
    }}
    // Run one dns action, then assert client.requests[1].Messages includes:
    // assistant ACTION_SELECTED/tool: dns/reason: resolve
    // user PENTGO TOOL EXECUTION RESULT/status/evidence
}
```

Add table tests for `TASK_COMPLETE` and `MISSION_COMPLETE`, each asserting a done state and one stop decision. Add a no-action test with three distinct text-only responses asserting done state and two `CONTINUE REQUIRED` messages before the third response completes. Add a repeatable-action fixture and tests asserting a third identical fingerprint appends `STRATEGY CHANGE REQUIRED`, while the fifth identical response stops before its fifth action runs. Keep unknown-action, duplicate non-repeatable action, cancellation, evidence failure, and max-turn failure coverage; update each fixture for `agent.Response`.

- [ ] **Step 2: Run the new runner tests and verify they fail**

Run:

```bash
go test ./tests/packages/internal/redteam/phases/recon -run 'TestAgentRunner' -count=1
```

Expected: FAIL because `AgentRunner` still calls `Decide`, advertises `recon_complete`, and has no transcript stop handling.

- [ ] **Step 3: Implement the transcript-driven loop**

In `Run`, create `transcript := newAgentTranscript(input)`, `noActionCount := 0`, and a recent fingerprint slice. Replace the decision call with:

```go
response, err := r.client.Respond(ctx, r.buildRequest(input, executed, transcript.Messages()))
```

Append non-empty response content as an assistant transcript message. Check `TASK_COMPLETE` and `MISSION_COMPLETE` case-insensitively before processing a decision; call `r.complete(state, response.Content)` on either marker. Record the response fingerprint before action execution. On five identical consecutive fingerprints, call `r.complete` before executing the incoming action. On three identical consecutive fingerprints, remember to append `renderAgentStrategyChange()` after the current action result.

For a non-nil `response.Decision`, reset `noActionCount`, then call a new `prepareAction(input, decision, executed) (AgentAction, PlannedCall, error)` helper. It performs the current registered-action lookup, non-repeatability check, `Plan` argument validation, and non-empty reason validation without mutating State. After preparation succeeds, append the always-synthesized `ACTION_SELECTED` assistant message with `plan.Reason`, then call `executeOne(ctx, state, action, plan, decision.Arguments, executed)`. Change `executeOne` to return `(agent.Message, error)` after `FinishCall`/`FinishBatch`; construct that message from the persisted `Call` identity and final `CallOutcome` using `renderAgentExecutionResult`. This fixes the transcript order as assistant decision first, user execution result second.

For a nil decision without a marker, increment `noActionCount`. On the third such response, call `r.complete(state, "Agent completed after three consecutive no-action responses.")`; otherwise append `renderAgentContinueRequired()` and begin the next turn.

Replace `complete(state, decision)` with `complete(state, summary string)`, bounded through the transcript helper before recording a model stop decision. Remove `reconCompletionDefinition`, `validateReconCompletion`, `renderAgentContext`, `boundAgentContextText`, and their obsolete context constants. Change `buildRequest` to accept transcript messages and expose only `availableDefinitions`; do not append a completion tool.

- [ ] **Step 4: Update the Recon skill completion instruction**

Change `skills/recon/SKILL.md` so its final guidance reads:

```text
证据已足以完成资产侦察时，在普通 assistant 文本中输出 TASK_COMPLETE 或 MISSION_COMPLETE，并提供简要总结。没有充分证据时继续选择一个有助于减少不确定性的已注册动作。
```

Update `tests/_packages/skills/registry_test.go` to assert `TASK_COMPLETE` and `证据`, rather than `recon_complete`.

- [ ] **Step 5: Run focused Recon and skills tests**

Run:

```bash
go test ./tests/packages/internal/redteam/phases/recon ./skills -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the Bingo-style runner loop**

```bash
git add internal/redteam/phases/recon/agent_runner.go skills/recon/SKILL.md tests/packages/internal/redteam/phases/recon/agent_runner_test.go tests/_packages/skills/registry_test.go
git commit -m "feat: run recon through Bingo-style transcript loop"
```

### Task 5: Update User Documentation and Verify the Complete Change

**Files:**
- Modify: `README.md:43,111-113`
- Modify: `docs/superpowers/specs/2026-07-16-recon-bingo-style-loop-design.md` only when implementation reveals a factual mismatch; otherwise retain the approved specification unchanged.

**Interfaces:**
- Consumes: final Agent protocol and loop behavior from Tasks 1-4.
- Produces: user-facing configuration and behavior documentation that matches the running agent.

- [ ] **Step 1: Update the Agent behavior paragraph in README**

Replace the `recon_complete` wording with a concise description that Agent mode keeps an in-memory assistant-decision and tool-result transcript, sends the bounded result text to the next model turn, accepts `TASK_COMPLETE` or `MISSION_COMPLETE`, and also ends after its Bingo-style no-action or hard-stuck conditions. Keep the existing statement that actions stay registered and locally validated. Update the DeepSeek paragraph to retain `thinking_mode: "disabled"` because this loop does not retain and resend provider-native `reasoning_content` across tool turns; do not describe the reason as a forced `tool_choice` restriction.

- [ ] **Step 2: Verify no retired completion tool references remain in runtime documentation or Go code**

Run:

```bash
rg -n 'recon_complete|\.Decide\(|UserPrompt' README.md internal skills tests/_packages tests/packages --glob '*.go' --glob '*.md'
```

Expected: no matches. Historical documents under `docs/superpowers/specs/2026-07-16-recon-agent-loop-design.md` and the user-modified old implementation plan are intentionally excluded from this check.

- [ ] **Step 3: Run formatting and full verification**

Run:

```bash
gofmt -w internal/agent/types.go internal/agent/openai.go internal/agent/anthropic.go internal/redteam/phases/recon/agent_runner.go internal/redteam/phases/recon/agent_transcript.go tests/_packages/internal/agent/client_test.go tests/_packages/internal/app/engagement_test.go tests/_packages/skills/registry_test.go tests/packages/internal/redteam/phases/recon/agent_runner_test.go tests/packages/internal/redteam/phases/recon/agent_transcript_test.go
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
go mod verify
git diff --check
```

Expected: every command exits successfully.

- [ ] **Step 4: Review the final diff and commit the documentation**

Run:

```bash
git diff -- README.md
git status --short
```

Confirm the user-modified `docs/superpowers/plans/2026-07-16-recon-repl-agent-loop.md` is not staged. Then commit only the README update:

```bash
git add README.md
git commit -m "docs: describe recon transcript loop"
```
