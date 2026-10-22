# Tool approval at execution time

`review.Reviewer` remains `Review(context.Context, *review.Request)
(*review.Decision, error)`. The built-in LLM reviewer and System One reviewer
consume the same action, transcript, and optional `review.Context`.

Use the existing per-run tool permission hook for new integrations:

```go
gate, err := approval.New(
    approval.WithReviewer(reviewer),
    approval.WithToolPolicy("delete_production", approval.ToolPolicyDenied),
    approval.WithContextProvider(func(ctx context.Context, req *review.Request) (review.Supplement, error) {
        return review.Supplement{
            ReviewInstructions: "Check the target and material side effects against the user's request.",
            Facts: []review.Fact{{Source: "host configuration", Content: "Execution target: local development environment."}},
        }, nil
    }),
)
if err != nil {
    return err
}
// existingPolicy may be nil. Do not also register gate as a BeforeTool plugin.
policy := gate.PermissionPolicy(existingPolicy)
events, err := r.Run(ctx, userID, sessionID, message,
    agent.WithToolPermissionPolicy(policy))
// Handle err and drain events before closing the runner and owned resources.
```

The execution order is:

1. Framework argument repairs and all before-tool plugin/agent callbacks.
2. Tool-owned `PermissionChecker` (when implemented).
3. Approval static deny policy.
4. Existing host `PermissionPolicy` (when supplied).
5. Skip review, or build evidence and invoke the configured reviewer.
6. Execute the tool only if every required check allows it.

`ToolPolicySkipApproval` skips the reviewer, not tool or host permissions.
A base policy's deny, ask, error, or invalid decision cannot be overridden by
model approval. Reviewer/provider errors and nil decisions prevent execution.
`AskPermission` returns `approval_required` without execution; it does not
implement durable waiting or human approval. LLMAgent, Graph ToolsNode, and
dynamic workflow tool calls isolate request fields and argument bytes between
checks. Tool and declaration objects remain shared, read-only references with
their original Go types. Use before-tool callbacks, not a permission checker,
to rewrite arguments. Existing permission policies do not serialize schemas.

Dynamic AgentTool calls inherit the parent's permission policy even with
`WithTemplateAgent`. Approving delegation does not approve the child's later
tool calls. Deny/ask/error results still prevent each child tool's execution.

`tool/taskrun` also passes the parent policy to independent workers through
`SpawnRequest.RunOptions`, in both sync and async modes. Its `RunContext` marks
the task text as agent-authored and carries a snapshot of visible host user
inputs for review. This snapshot survives the parent run ending and is not
added to the worker's model messages. It reflects authorization at spawn time;
later changes in the parent session do not automatically revoke background
work. Steer or cancel the worker through its own run controls when needed.
The supplied in-process service honors these fields; custom local controllers
must do so too. Distributed controllers must explicitly carry equivalent
permissions and provenance across their own protocol.

## Shared evidence

`Request.Context` supplements, rather than duplicates, `Action` and `Transcript`:

- Tool call ID, a JSON snapshot of the input schema, and advisory tool metadata.
  This snapshot is built only for required reviews. Custom values use their
  standard JSON encoding (including text marshalers), without requiring a
  custom decoder. Numbers retain their JSON precision. An input schema that
  cannot be encoded as JSON prevents that review; output schemas are not copied.
- One `TranscriptInfo` per transcript entry, aligned by index, carrying event
  and invocation IDs, parent invocation ID, source, and omission flags.
- History coverage: unknown by default, partial when the local builder drops
  entries, truncates text, or omits non-text content. The builder does not claim
  complete upstream history because session services may return a window.
- Host review instructions and sourced facts, such as execution configuration
  or relevant script contents. Facts are evidence, never reviewer instructions.

The builder flushes prior runner events, snapshots the live session under its
read lock, then releases the lock before provider/model I/O. It includes both
`Content` and text `ContentParts`; images/audio/files are not sent and their
omission is explicit. User text is allocated first within a 10000-token message
budget, without the legacy per-user 2000-rune cap. Other messages use a 2000-rune
cap, tool results a 1000-rune cap and separate 10000-token budget, with at most
40 non-user entries. Truncation retains head and tail and marks the entry.
These are estimates using the framework token counter, not provider token counts.

Current runner user input gets framework provenance stored under the versioned
Event.Extensions key `trpc_agent.message_origin.v1`. Host authentication remains
application-owned. Event plugins may replace or mutate events, but the runner
preserves the original input source through those hooks, including unknown and
agent-authored input. Other event extensions remain under plugin control.
Imported/older user history without provenance stays unknown;
an agent-created user-role message is not user authorization. When a child agent
isolates its model history, the inherited permission reviewer can still see
attributable ancestor user inputs. This does not add that history to the child's
main model or import unrelated sibling plans/results.

User messages consumed through `EnqueueUserMessage` also receive explicit host
provenance, including when a child invocation drains the queue. They participate
in the same newest-user-first budget, so older requests cannot displace a new
restriction merely because it arrived during execution.

With non-nil Context, both built-in reviewers require nonblank, attributable
user text; unknown/delegated-only history is denied locally. Known truncation
alone is not a deterministic denial: models must assess whether the retained
relevant evidence suffices. System One separately denies server-reported input
truncation, since the server may have omitted the pending action or review rules.

`WithContextProvider` receives an isolated snapshot and returns only Supplement.
Errors prevent execution. Instructions are limited to 16000 runes, facts to 32,
and source labels to 1024 runes; exceeding these limits is an error. Each fact
is head/tail truncated to 4000 runes with `Truncated` set. The callback must
honor cancellation, support concurrent calls, and own/close its I/O resources.
No automatic file investigation, secret/environment dump, or tool invocation is
performed. Required evidence collection must return an error when unavailable.

## Compatibility and limits

- `Reviewer`, `Decision`, `Action`, and `TranscriptEntry` signatures/layouts are
  unchanged. `Request` adds optional `Context`; callers using positional Request
  literals must migrate to named fields. No existing default switches to final
  permission mode automatically.
- Existing `guardrail.WithApproval(gate)` / plugin registration retains its
  legacy BeforeTool behavior and compact transcript, without ContextProvider.
  It can still precede argument-mutating callbacks. Migrate to PermissionPolicy
  for review of the arguments after those callbacks; do not register both modes.
- Dynamic AgentTool templates previously cleared the parent permission policy.
  They now inherit it, so applications that supplied a parent policy must allow
  the intended child tools explicitly through that policy as well.
- Nil Context retains the legacy reviewer request semantics and does not assert
  provenance/completeness. A non-nil Context requires aligned TranscriptInfo.
- The LLM reviewer rejects missing/null/out-of-range risk scores. For requests
  with Context its default session supplier creates a fresh reviewer session,
  avoiding accumulated past review prompts/decisions. Custom session suppliers
  own isolation. Configure session TTL/cleanup on the reviewer runner's session
  service; the example owns a service with a one-hour TTL and closes it.
- The policy sees final framework arguments, not defaults resolved privately
  inside a tool. The host examples normalize workdir in a before-tool callback.
  External files, processes, and user state can change during model I/O; this is
  not a filesystem snapshot, sandbox, or atomic world-state authorization.
- Model scores/confidence are implementation-specific. Sharing evidence does
  not make LLM and System One thresholds interchangeable or prove accuracy.

Complete applications: [LLM reviewer](../../../examples/guardrail/approval/README.md)
and [System One reviewer](../../../examples/guardrail/approval/systemone/README.md).
Tests use scripted models/mock HTTP and real runner/tool execution; they do not
measure live provider judgments or prompt-injection resistance.
