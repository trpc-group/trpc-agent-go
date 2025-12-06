# concurrency_race example

This example verifies that **Pregel channel state is isolated per run when a single GraphAgent/Runner is reused concurrently**.

## Scenario

- Graph structure: `start -> worker`
- The `worker` node increments the `counter` field in state by 1 on each execution.
- The example:
  - Creates a single global `GraphAgent` and `Runner`;
  - Starts `32` goroutines and runs `64` rounds (2048 runs in total) against the same Runner;
  - For each run, reads the final `counter` value from the `graph.execution` completion event.

If channel state is incorrectly shared at the Graph level between runs, you will observe:

- Some runs with `counter == 0`: the `worker` node was never scheduled in that run;
- Some runs with `counter > 1`: the `worker` node was triggered multiple times in a single run.

With the current implementation, channel state lives in the per‑run `ExecutionContext`, so under reasonable concurrency all runs should satisfy:

- `final_counter == 1`

Any deviation indicates a concurrency bug in channel isolation.

## How to run

From the repository root:

```bash
cd examples/graph
go run ./concurrency_race
```

This example does not depend on any LLM or external service; it only requires that `trpc-agent-go1` builds successfully.

## Interpreting the output

- Healthy output (no concurrency issues observed):

  ```text
  🚀 concurrency_race example: 32 goroutines × 64 rounds
  ✅ No missing worker executions observed (try increasing rounds/concurrency if needed).
  ```

- If there are problematic runs, you will see lines like:

  ```text
  ❌ Detected N runs where worker node did not execute:
     - round=0 idx=3 session=... final_counter=0 (expected 1)
     - round=1 idx=10 session=... final_counter=3 (expected 1)
     ...
  ```
You can increase pressure by tweaking `defaultConcurrency` and `defaultRounds` at the top of `main.go`. This makes it useful as a regression test for any changes touching Pregel channel isolation.
