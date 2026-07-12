# Design Notes

This example keeps the optimization loop at example scope while still exercising the production path that matters for prompt updates. The fake mode builds a real `llmagent`, `runner.NewRunner`, `evaluation.AgentEvaluator`, and PromptIter engine. Only the model and PromptIter workers are deterministic fakes, so a patch must still pass through the existing profile and surface-patch machinery before it can affect candidate inference.

Failure attribution is derived from evaluator output rather than hidden test state. The deterministic metrics always emit stable failure reasons, and the report maps those reasons plus the terminal execution trace step into categories such as `tool_not_called`, `route_error`, `tool_arguments_mismatch`, and `final_response_mismatch`. A failed metric with an empty reason is rejected because it cannot produce an auditable explanation.

PromptIter acceptance and the final gate deliberately make different decisions. PromptIter decides whether a round's patch becomes the accepted profile based on score gain. The final gate then asks whether the accepted profile should be published. It checks validation gain, new hard failures, critical case regressions, latency, and fake cost. This separation lets the demo show a realistic overfitting case: train and aggregate validation scores improve, but a held-out critical case regresses, so the final gate rejects the candidate.

Trace smoke mode validates a different boundary. The framework natively supports `evalMode: "trace"`, where recorded actual invocations are replayed instead of running live inference. This confirms that trace evalsets can be evaluated, attributed, and rendered in the same report shape, but it intentionally skips optimization because replayed actual output cannot prove that a prompt patch would change the next candidate run.

| Mode | Optimization fields | Trace smoke fields | Release signal |
| --- | --- | --- | --- |
| `fake` | populated | `enabled=false` | final gate decision |
| `trace-smoke` | empty or nil | populated | none, optimization skipped |

The JSON and Markdown reports are audit artifacts. They record deterministic seed `0`, prompt and configuration sources and hashes, resolved PromptIter policies, fake model generation settings, target surfaces, round patches, scores, deltas, gate reasons, attribution evidence, latency, model calls, and deterministic zero cost. An omitted critical-case list retains the example default, while an explicit empty list disables that check without changing other gate rules. This makes the example reproducible without an API key while leaving a clear path for future real-model extensions.
