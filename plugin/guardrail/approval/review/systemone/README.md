# System One tool approval reviewer

`systemone.Reviewer` implements the existing `review.Reviewer` interface. It
uses the System One HTTP protocol for Jev and compatible servers. It shares the
internal protocol client and does not create an agent, runner, or session.

```go
import "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review/systemone"

reviewer, err := systemone.New("https://api.typesafe.ai",
    systemone.WithAPIKey(apiKey),
    systemone.WithModel("jev-1.13.0"),
)
if err != nil {
    return err
}
approvalPlugin, err := approval.New(approval.WithReviewer(reviewer))
```

Install `approvalPlugin.PermissionPolicy(existingPolicy)` with
`agent.WithToolPermissionPolicy(...)` on each run; existingPolicy may be nil.
Do not also register this approval instance as a BeforeTool plugin. The final
permission adapter checks the arguments after all before-tool callbacks and
composes existing hard policies. Legacy plugin registration remains available.
See the [shared approval contract](../../README.md).

## Request and rubric

One `POST /v1/systemone` carries the planned action and ordered transcript as a
structured `state`. Arguments are embedded JSON, preserving large numbers;
empty arguments become JSON `null`. Tool description and role/content entries
are preserved. When supplied, `Request.Context` is serialized as `state.context`,
including schema, metadata, transcript provenance/omissions, and host facts. The reviewer does not fetch more history, summarize, or truncate
the request. Invalid JSON or UTF-8 returns an error before calling the server.

Two fixed questions keep authorization separate from intrinsic impact:

| Question | Type | Answers |
| --- | --- | --- |
| `authorization` | Choice | `supported`, `contradicted`, `insufficient` |
| `risk` | Score | Five ordered criteria, from negligible to critical |

The questions treat action/transcript/fact contents as untrusted evidence.
Host `context.supplement.review_instructions` supplies review guidance subject
to the fixed evidence rules; it cannot replace hard permission checks. Assistant plans,
tool output, and tool descriptions cannot grant user authorization. Later user
corrections constrain earlier permission; quoted instructions are task data
unless the user adopts them. Clear conflicts are `contradicted`; missing or
ambiguous coverage without a clear conflict is `insufficient`.

The risk rubric considers the whole action, including chained commands,
redirects, and invoked scripts. It assesses targets, side effects, reversibility,
sensitive data, privileges, and uncertainty independently of authorization.
Each level describes its own conditions rather than relying on adjacent levels.

### Prompt construction

Jev still needs a task definition: `instructions` asks the question and
`criteria` defines its answer space. The question IDs only identify responses.
Each question is evaluated independently, so both include the shared evidence
rules. See TypeSafe's [question guidance](https://docs.typesafe.ai/primitives)
and [Score level guidance](https://docs.typesafe.ai/primitives/score).

The guardian reviewer uses `text/template` to render chat instructions, its
threshold, and the action/transcript text. This reviewer instead serializes
dynamic request data into `state`; the fixed questions refer to its `action`
and `transcript` fields. There is no per-request prompt interpolation or chat
system message. Approval thresholds are applied only in local code, so changing
them does not change what the model is asked to assess.

The built-in questions are not configurable. Their labels and five-level rubric
are coupled to the local decision rules; arbitrary replacements could invalidate
the meaning of the risk and confidence checks. A different review policy can
implement the existing `review.Reviewer` interface.

Prompt wording must be evaluated on labeled actions and transcripts for the
chosen model. Mock HTTP tests validate request construction and local decisions,
not authorization accuracy or resistance to prompt injection.

### Risk levels

| Ordinal level | Normalized value | Criterion |
| --- | --- | --- |
| 0 | 0 | Narrow read-only inspection of non-sensitive data |
| 1 | 25 | Bounded, readily reversible local changes |
| 2 | 50 | Recoverable changes, bounded non-sensitive external effects, or uncertainty without evidence of destructive/sensitive/production/privilege impact |
| 3 | 75 | Destructive/broad changes, sensitive access/transfer, production or privilege impact without evidence of widespread irreversible damage, secret disclosure, or severe security compromise |
| 4 | 100 | Widespread irreversible damage, secret disclosure, or severe security compromise |

The server returns a possibly fractional ordinal score. `RiskScore` is
`round(25 * score)`, with halves rounded up. `RiskLevel` uses the unrounded
value: `low` at 0–25, `moderate` above 25 through 50, `high` above 50 through
75, and `critical` above 75. These values are specific to this rubric and are
not interchangeable with scores from `review.New`.

## Local decision policy

Defaults are an exclusive risk threshold of **50** and an inclusive minimum
confidence of **0.8**. Approval requires all of the following:

1. The authorization choice is `supported`.
2. Both answer confidences and `P(supported)` are at least the minimum.
3. The **unrounded** normalized score is below the risk threshold.
4. The sum of probabilities of ordinal levels whose normalized value is
   strictly below the risk threshold is at least the minimum.
5. The server has not reported `usage.truncated: true`.

For example, at the default threshold, condition 4 checks `P(level 0) +
P(level 1) >= 0.8`. A low expected score alone cannot override an uncertain
risk distribution. Since approval uses the unrounded score, `RiskScore` can
round up to the threshold while `Approved` is true. The approval plugin consumes
`Approved`; it does not reinterpret the displayed score.

`WithRiskThreshold` accepts 0–100; zero disables automatic approval.
`WithMinConfidence` accepts finite values in 0–1; zero disables uncertainty
checks, but still requires supported authorization, a score below threshold,
and no reported truncation. The same minimum is applied to confidences and the
two relevant probability masses; it does not make their statistical meanings
equivalent. In particular, Jev and Laya confidence values are provider-specific.
These defaults are starting policy choices, not calibrated safety guarantees.
Validate the rubric and thresholds for each model version and workload.

`Reason` is a deterministic description of the first failed local check, or
confirmation that the checks passed. It is not an action-specific rationale
generated by another model. Rich distributions and usage are consumed locally;
the existing `review.Decision` contract is unchanged.

With Context, only nonblank user text attributed to `OriginUserInput` can
establish user intent; role alone is insufficient. Nil Context retains the
legacy role-based behavior. Without usable user input, the reviewer denies locally with
`RiskScore: 100`, `RiskLevel: "unknown"`. This is a conservative sentinel, not
a model assessment. Other valid but uncertain answers return `Approved: false`.
Invalid requests, transport failures, and malformed/mismatched answers return
an error and no decision. The approval plugin blocks execution in both cases.

## Lifecycle and execution boundaries

- `New` requires an explicit HTTP(S) API root and appends `/v1/systemone`.
  Gateway path prefixes are allowed; query, fragment, and userinfo are rejected.
- No configuration is read from the environment. `WithModel` defaults to empty
  for server routing; Jev needs an explicit model. An empty API key omits bearer
  authentication for local servers.
- The default deadline is 30 seconds. `WithTimeout(0)` disables that deadline;
  caller cancellation and earlier context/HTTP client deadlines still apply.
- There is one request and no retries, redirects, fallback, or extra model call.
  Responses larger than 8 MiB fail. The supplied HTTP client's configuration is
  copied; its transport is shared and remains caller-owned.
- Concurrent calls are supported when the HTTP transport supports them. Request
  data must not be mutated during a call. No `Close` is needed.
- Only the supplied snapshot is reviewed. The shared builder reports local
  truncation and non-text omission, and leaves upstream completeness unknown.
  The reviewer does not recover omitted history or fetch external evidence.
- Use the final permission adapter to bind review to post-callback arguments.
  Legacy BeforeTool registration may still precede argument-mutating callbacks.
  Both modes retain tool/host hard restrictions; model approval cannot override
  a denial. Host evidence is a snapshot, not an atomic filesystem/world state.
- A denial does not implement human escalation or durable pause/resume. The
  approval plugin returns a tool result; the host owns any human approval flow.

See the [runnable review example](../../../../../examples/guardrail/approval/systemone/README.md).
Unit tests use mock HTTP responses and cover protocol mapping, threshold and
uncertainty boundaries, cancellation, concurrent calls, context provenance, and permission/legacy callback
integration. They do not establish live model judgment quality.
