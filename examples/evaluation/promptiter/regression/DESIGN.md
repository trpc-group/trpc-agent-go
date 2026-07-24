# Design

The regression loop keeps optimization and acceptance as separate
responsibilities. Evaluation Service is the source of truth for quality: every
baseline and candidate run emits a final response, tool trajectory, and
execution trace, then the configured built-in metrics produce per-case scores
and pass/fail states. The loop does not infer success from optimizer output.
This makes fake runs and model-backed runs comparable and prevents a candidate
from bypassing evaluation.

Failure attribution is deterministic and evidence based. It first inspects
expected and actual knowledge-search calls, structured JSON validity, tool
names and arguments, trace routing errors, and metric reasons. A generic final
response mismatch is used only after more specific signals have been checked.
Every failed case receives at least one category and human-readable evidence;
the first category is the primary attribution used in summary counts. The full
list remains in the JSON report so ambiguous failures can be audited instead
of being forced into one label.

Candidates use PromptIter's `SurfacePatch` and `Profile` contracts for the
target instruction surface. In deterministic mode, configured patch text
replaces the LLM-backed backward, aggregation, and optimizer stages. This keeps
the example reproducible without credentials while preserving the integration
boundary: production code can take candidate profiles from the normal
PromptIter engine and reuse the same evaluation, delta, gate, and reporting
layer.

Acceptance is conjunctive. Validation gain must meet the configured threshold,
new hard failures are rejected unless explicitly allowed, critical cases may
not drop beyond their limit, and estimated cost and tool-call counts must stay
within budget. Each candidate is compared with the currently accepted prompt,
not merely the original baseline. The report also includes an original-to-final
delta for release review.

Overfitting is controlled by never using validation failures to generate the
next candidate. Candidate targeting reads only training attributions. Every
candidate still runs on the held-out validation set, where case-level deltas
expose newly passed, newly failed, improved, regressed, and unchanged cases.
The included second round intentionally improves training while damaging
validation, proving that the gate rejects this pattern.

Audit artifacts record all candidate prompts, metric results, traces, deltas,
gate checks, rejection reasons, token estimates, tool calls, latency, seed, and
fake model pricing. JSON supports automation; Markdown supports reviewer
approval. Source prompt write-back is opt-in and occurs only after acceptance,
so a generated candidate cannot silently enter production.
