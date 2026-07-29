# PentGo Evidence Slimming Design

**Date:** 2026-07-29

**Status:** Approved in discussion; pending implementation planning.

## Goal

Reduce PentGo to one Eino single-agent runtime with three explicit output channels:

1. Action-tool observations are appended to one `evidence.jsonl` file.
2. Confirmed findings are recorded during the run with `record_finding`.
3. A normal assistant response ends the run and becomes the final summary.

The runtime then renders `report.md` deterministically from the recorded findings and final summary.

## Design Principles

- Follow CyberStrikeAI's separation between tool execution, formal findings, and the final assistant response.
- Keep only mechanical validation in the runtime. The Agent is responsible for using tools to verify a finding before recording it.
- Use engagement-local files, not SQLite or a storage abstraction.
- Keep one execution model: Eino native tool calls for both OpenAI and Anthropic.
- Prefer deletion over compatibility layers. This prototype does not preserve old evidence artifacts or completion protocols.

## Non-Goals

- No database, migrations, cross-engagement search, management UI, or retention service.
- No independent vulnerability replay, baseline comparison, reproduction counter, confidence score, or framework verdict.
- No `VERIFIED`, `LIKELY`, or `INFERRED` evidence levels.
- No Finding DSL, report-model call, declaration audit, or report-context compaction.
- No `complete_task`, `evidence_gate`, minimum-evidence gate, or text completion tokens.
- No `declare_session`, session pool, login verifier, or framework Cookie injection.
- No compatibility output for per-block evidence JSON or verification JSON.

## Runtime Flow

```text
user task
  -> one Eino ChatModelAgent run
  -> exec / execute_python / specialized MCP tool
  -> append action result to evidence.jsonl
  -> return the same result plus [evidence_ref: N] to the Agent
  -> record_finding when the Agent has verified a finding
  -> continue using tools as needed
  -> assistant emits normal text without tool calls
  -> Eino naturally ends
  -> save final_summary
  -> render session.json and report.md
  -> remove registered Runtime script files
  -> atomically publish the engagement directory
```

The runtime does not judge whether the Agent worked long enough. The system prompt defines CAI-style stopping guidance, while Eino `MaxIterations` remains the only loop bound.

## Evidence Journal

### Artifact

Each engagement has exactly one raw evidence file:

```text
evidence.jsonl
```

Each completed action-tool call appends one JSON object followed by a newline:

```json
{
  "seq": 3,
  "tool": "execute_python",
  "arguments": {
    "script": "PAYLOAD"
  },
  "success": true,
  "output": "RESULT\n[evidence_ref: 3]",
  "started_at": "2026-07-29T12:00:00Z",
  "finished_at": "2026-07-29T12:00:02Z"
}
```

The example is expanded for readability. The file uses compact JSON encoding so every record occupies exactly one physical line. The Journal creates the empty file at engagement start even when the Agent finishes without calling an action tool.

### Recording Rules

- Record `exec`, `execute_python`, and every discovered specialized MCP tool.
- Do not record control tools such as `load_skill` and `record_finding`.
- Record failed, rejected, timed-out, and cancelled action calls with `success: false`.
- A local call is successful only when its execution status is `succeeded`.
- An MCP call is successful only when transport invocation succeeds and the MCP result has `isError: false`.
- Apply output limits and redaction before recording. The stored `output` is the bounded text returned to the Agent, including its evidence reference.
- Store arguments as the action tool received them. The script or command in the arguments replaces separately published Runtime script evidence.
- Assign `seq` under the Journal mutex when the completed call is appended. Therefore sequence order is completion order.
- The Journal keeps an in-memory `seq -> success` index for `record_finding` validation.
- A Journal append error ends the engagement with a run error. The tool result is not returned without its durable record.

`EvidenceJournal` is a concrete engagement-local file object. It owns sequence allocation, serialized append, and reference lookup. No storage interface or alternate backend is introduced.

## Finding Tool

`record_finding` is an ordinary non-exit Eino tool:

```json
{
  "title": "Management endpoint permits unauthorized access",
  "severity": "high",
  "description": "A low-privilege request returned management-only data and fields.",
  "evidence_refs": [3, 5],
  "recommendation": "Enforce role and resource authorization on the server for this endpoint."
}
```

The five fields are required. `severity` accepts only:

```text
critical, high, medium, low, info
```

Runtime validation is deliberately mechanical:

- Trim and require `title`, `description`, and `recommendation`.
- Require at least one evidence reference.
- Require every referenced sequence to exist and have `success: true`.
- Reject duplicate references within one finding.
- Reject a title already recorded in the engagement after case-folding and trimming.

Validation failures are soft tool errors so the Agent can correct and retry. A successful call appends the finding to the in-memory session and returns `finding #N recorded`. The call itself is not appended to `evidence.jsonl`.

There is no semantic vulnerability validation after `record_finding`. The runtime does not replay requests, infer vulnerability types, assign confidence, or promote a status. Report wording calls these entries "Agent-recorded findings."

## Natural Completion

PentGo follows CAI's natural Eino completion behavior:

- A response containing tool calls continues through the Eino ToolsNode.
- The first final assistant response without tool calls ends the engagement.
- That response is stored verbatim as `final_summary` after trimming.
- No action-tool call or finding is required before the Agent may finish.
- An empty final response produces `failed` with stop reason `empty_response`.
- Exhausting Eino iterations produces `failed` with stop reason `max_iterations`.
- Cancellation and provider errors preserve their existing distinct terminal states.

The current outer Eino re-drive loop is removed. Refusal counters, no-code counters, evidence reminders, stuck fingerprints, `complete_task`, and `evidence_gate` are not part of the replacement path.

## Published Artifacts

The final directory is:

```text
eng-*/
|-- evidence.jsonl
|-- session.json
|-- report.md
`-- work/
```

### Session

`session.json` is a compact terminal-state document:

```json
{
  "id": "eng-ID",
  "target": "TARGET",
  "intent": "TASK",
  "status": "done",
  "stop_reason": "agent_finished",
  "turns": 8,
  "started_at": "TIME",
  "finished_at": "TIME",
  "findings": [],
  "final_summary": "Agent final response"
}
```

It does not persist the old timeline, loaded-skill list, login sessions, verification results, confidence, or evidence levels. Runtime progress callbacks may continue for terminal display but are not a second persisted evidence stream.

### Report

`report.md` is generated locally without a model call. It contains:

```markdown
# PentGo Report

## Task
Target, intent, status, stop reason, and timestamps.

## Findings

### [HIGH] Management endpoint permits unauthorized access
Description...

Evidence: `#3`, `#5`

Recommendation: ...

## Agent Summary
The final assistant response.
```

Findings render in recording order. When there are none, the report says that no findings were recorded. The findings section always comes before the Agent summary so a final response cannot omit previously recorded findings from the published report.

### Work Directory

The work directory remains available during the engagement and may contain files intentionally produced or downloaded by the Agent. The executor registers every Runtime-generated `turn-*.py` and `turn-*.sh` path when it creates the script. Before publication, the writer removes only those registered paths instead of deleting by a broad glob. Other work files are published.

## Component Boundaries

- `EvidenceJournal`: concrete JSONL writer, sequence allocator, and reference index.
- Local action tools: execute through the existing preflight, authorizer, timeout, cancellation, output-limit, and redaction path, then record the final result in the Journal.
- MCP adapters: invoke the remote tool, normalize its text/error result, apply the same output bound, then record it in the Journal.
- `record_finding`: validates Journal references and mutates the current session finding list.
- Eino runner: consumes one ADK run, tracks turns, and captures the natural final assistant response.
- Engagement writer: renders compact session/report files, removes registered Runtime scripts, and atomically publishes the staging directory.

## Removal Scope

Remove the following behavior and its tests:

- The legacy `Runner.Run` text/code-fence loop and its text completion protocol.
- Markdown code-block extraction used only by that loop.
- `complete_task`, `evidence_gate`, and the evidence-gate middleware.
- Eino outer re-drive logic for refusal, no-code, no-evidence, and stuck recovery.
- `declare_session`, session declarations, session pool, login verification, and Cookie environment injection.
- Per-block `executionEvidence`, `EvidenceSink`, and `evidence/agent-turn-*.json` output.
- `EvidenceLevel`, `GradeEvidence`, model finding-label extraction, and report validation.
- Finding consolidation prompts, Finding DSL parsing, vulnerability HTTP verification, reproduction scoring, and `verification-*.json` output.
- Report-model generation, `ReportContext`, and bounded report-prompt assembly.
- Configuration used only by removed behavior, including maximum findings, verification reproductions, no-code recovery, block-per-turn parsing, provider text-loop delay, and stuck thresholds.

Retain:

- Eino model construction for OpenAI and Anthropic.
- `exec`, `execute_python`, specialized MCP tools, `load_skill`, and `record_finding`.
- Preflight repair/rejection, target scope authorization, execution timeout, cancellation, output caps, repeat-output protection, and redaction.
- Eino maximum iterations and network backoff where action tools still use them.
- Atomic engagement staging and publication.

## Interaction With the Minimal MCP Plan

This design supersedes the completion/evidence portions of `2026-07-29-minimal-mcp-execution-tools.md`:

- Do not add `MCPEvidenceSeen` or any MCP success callback for a completion gate.
- Do not retain or extend `complete_task`/`evidence_gate` behavior.
- Give the MCP adapter the same concrete EvidenceJournal used by local action tools.
- Append successful and failed MCP results to `evidence.jsonl` and return their evidence references.
- Keep the one-server stdio boundary, tool discovery, collision checks, and split `exec`/`execute_python` work from that plan.

Because both changes touch the Eino tool set, runner, engagement service, prompt, and tests, their implementation plans must be reconciled before execution rather than run independently as written.

## Error Handling

- Action failure: append `success: false`, return the bounded error result with its evidence reference, and let the Agent choose the next action.
- Journal failure: return a run error and publish the terminal failed session if publication remains possible.
- Finding validation failure: return a soft tool error and keep the Eino loop running.
- Empty natural completion: fail with `empty_response`.
- Iteration exhaustion: fail with `max_iterations`.
- Context cancellation: cancel active local/MCP calls and publish records already completed.
- Provider error: fail the session and publish records already completed.
- Report rendering: deterministic local rendering; only local filesystem errors remain.
- Runtime-script cleanup failure: treat publication as failed rather than silently publishing files declared temporary.

## Test Strategy

1. Concurrent Journal appends produce unique sequences and independently parseable JSONL lines.
2. Local `exec` and `execute_python` calls use the same record schema for success, rejection, timeout, cancellation, and failure.
3. MCP success and `isError` responses use the same schema through a local stdio fixture.
4. The exact returned tool text contains the same evidence reference stored in the Journal output.
5. `record_finding` rejects missing fields, invalid severities, empty references, nonexistent references, failed-call references, duplicate references, and duplicate titles.
6. `record_finding` succeeds for valid references and does not end the Agent run.
7. A tool call followed by `record_finding` and a normal assistant response naturally completes the engagement.
8. A normal assistant response without any tool call also naturally completes the engagement.
9. Empty completion, maximum iterations, provider error, and cancellation produce their specified terminal states.
10. Deterministic reports contain all recorded findings before the final summary and handle zero findings.
11. Failed and cancelled engagements publish all Journal records completed before termination.
12. Publication removes only registered Runtime scripts and preserves Agent-created work files.
13. Network-facing tests use local `httptest` servers and local stdio fixtures only.

## Acceptance Criteria

- A normal assistant text response is the only successful completion signal.
- Every action-tool result seen by the Agent has exactly one `evidence.jsonl` record and sequence reference.
- Every published finding references one or more successful Journal entries.
- No runtime vulnerability replay or evidence-level grading remains.
- No report model is called.
- No per-call evidence JSON or verification JSON is published.
- No framework-managed login session or Cookie injection remains.
- The published report is reproducible from `session.json` and `evidence.jsonl` without model access.
- The implementation uses files only and introduces no database or management layer.
