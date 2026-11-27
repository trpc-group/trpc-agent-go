---
name: fanout-analysis
description: Use this workflow recipe when a question benefits from several independent perspectives followed by a single evidence-based synthesis.
---

# Parallel Analysis and Synthesis

Turn the user's request into a temporary fan-out/fan-in workflow. Keep the
number and focus of branches appropriate to the request; do not create roles
just to make the workflow look larger.

## Process

1. Extract the decision or question, the relevant constraints, and the output
   format from the user's request.
2. Choose two to four independent analysis angles that cover different
   evidence or reasoning needs. Give every branch the same core question and
   only the context it needs. Do not let one branch depend on another branch's
   unfinished answer.
3. Run those branches in parallel. Each branch should return concise findings,
   assumptions, and unresolved uncertainty rather than a polished final
   answer.
4. Pass the ordered branch results, the original request, and the explicit
   decision criteria to a separate synthesis role. The synthesizer must
   distinguish agreement, disagreement, and missing evidence; it must not
   silently turn an unsupported claim into a fact.
5. If the request requires a decision, have the synthesizer return a small
   structured object with `recommendation`, `reasons`, and `uncertainties`.
   Keep branch content as text unless a later control-flow decision genuinely
   needs typed fields.
6. Return the synthesis and the key evidence trail. If a branch fails, keep
   that missing evidence explicit and follow the application's bounded failure
   policy; do not silently treat it as support or retry indefinitely.

## Compilation Rules

- Express independent branches with `parallel([...])`; preserve the input
  order when passing results to the synthesizer.
- Use separate workflow-local Agent instances for each branch and for the
  synthesizer. Do not ask the synthesizer to redo every branch from memory.
- Pass the original question, constraints, and branch outputs explicitly as
  inputs. A later stage must not depend on context that was only present in a
  previous Agent's prompt.
- `parallel` returns `None` for a failed independent branch. Handle that value
  explicitly, and let the workflow or its caller decide whether a single,
  bounded rerun is appropriate; do not rely on exception-catching syntax or
  unbounded retries.
- Keep the glue code small: create roles, pass JSON-compatible values, fan out,
  fan in, and return the result. Delegate substantive analysis to Agents.
- Use `tools=[]` for roles that only reason over supplied inputs. Select a
  declared tool only for a branch whose task genuinely needs it, and keep
  mutating tools out of parallel branches unless their independence is clear.
- If a structured synthesis is requested, read the Agent result's explicit
  `structured` object. Do not ask for JSON-looking text and parse it in the
  workflow.
