# Design

The regression loop deliberately remains an example-level internal package. PromptIter owns optimization and Evaluation Service owns inference and scoring; this layer owns release decisions and audit artifacts. Keeping those responsibilities separate avoids another optimizer API and preserves existing framework behavior.

Every candidate is compared twice. The original-baseline delta explains cumulative improvement, while the accepted-baseline delta drives the gate and prevents a rejected candidate from contaminating later rounds. Before comparison, set, case, and metric identities are checked for emptiness, duplication, and completeness. Missing validation is rejection, never an implicit zero or pass.

Failure attribution combines metric names and reasons with trace status and failed tool steps. Gates require the configured score gain and reject new failures, metric regressions, critical-case regressions, or budget overruns. PromptIter's own acceptance is also mandatory. Reports contain both deltas, attribution, usage, and every rejection reason. JSON and Markdown are written through temporary files and renamed only after complete writes. Prompt writeback is a separate guarded operation that requires an accepted report and a matching text surface.

The deterministic runner invokes the actual PromptIter engine with offline implementations of its evaluator, backwarder, aggregator, and optimizer collaborators. Separate success, ineffective, and overfit scenarios exercise the full engine and report path without credentials, network access, nondeterministic judges, or production prompts.
