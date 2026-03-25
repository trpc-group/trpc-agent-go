# Graph Execution Trace Example

This example demonstrates how to enable execution trace collection for a `GraphAgent`, run a graph with real control flow, and print the resulting trace in a user-friendly format.

## What it demonstrates

- A single run that contains:
  - fan-out
  - fan-in
  - conditional routing
  - a loop
  - repeated node execution
  - a static node that is present in the graph but not executed in this run
- Collecting the trace from the runner completion event
- Printing:
  - basic trace metadata
  - step order
  - direct trace edges
  - repeated nodes and skipped nodes

## File layout

- `main.go`: Program entrypoint
- `agent.go`: Graph construction and `GraphAgent` creation
- `print.go`: Execution trace formatting and user-friendly output

## Run

From the repo root:

```bash
cd examples/graph
go run ./execution_trace
```

This example only uses function nodes. It does not require any API credentials.

## What to look for

The output is divided into three parts:

- `Step order`: The real steps executed in this run, with predecessors and summarized input/output snapshots
- `Trace edges`: Direct step-to-step dependencies derived from `PredecessorStepIDs`
- `Summary`: Repeated nodes and nodes that exist statically but were not executed in this run

Typical highlights in this example include:

- `assistant/start#1` fans out to both `assistant/prepare#1` and `assistant/route#1`
- `assistant/route#1` loops through `assistant/tools#1` before `assistant/route#2`
- `assistant/branch_a#1` and `assistant/branch_b#1` fan in to `assistant/join#1`
- `assistant/branch_never` exists in the graph but is not executed in this run

## Example output

```text
GraphAgent execution trace
========================================================================
Root Agent: assistant
Session ID: session-1
Status: completed
Step Count: 9

Step order
1. assistant/start#1
   node: assistant/start
   predecessors: (root)
   input: user_input="hello graph trace", route_count=0, visited=[]
   output: visited=[start]
   error: (none)
2. assistant/prepare#1
   node: assistant/prepare
   predecessors: assistant/start#1
   input: user_input="hello graph trace", route_count=0, visited=[start]
   output: visited=[prepare]
   error: (none)
3. assistant/route#1
   node: assistant/route
   predecessors: assistant/start#1
   input: user_input="hello graph trace", route_count=0, visited=[start]
   output: route_count=1, visited=[route]
   error: (none)
4. assistant/branch_b#1
   node: assistant/branch_b
   predecessors: assistant/prepare#1
   input: user_input="hello graph trace", route_count=1, visited=[start, prepare, route]
   output: visited=[branch_b]
   error: (none)
5. assistant/tools#1
   node: assistant/tools
   predecessors: assistant/route#1
   input: user_input="hello graph trace", route_count=1, visited=[start, prepare, route]
   output: visited=[tools]
   error: (none)
6. assistant/route#2
   node: assistant/route
   predecessors: assistant/tools#1
   input: user_input="hello graph trace", route_count=1, visited=[start, prepare, route, branch_b, tools]
   output: route_count=2, visited=[route]
   error: (none)
7. assistant/branch_a#1
   node: assistant/branch_a
   predecessors: assistant/route#2
   input: user_input="hello graph trace", route_count=2, visited=[start, prepare, route, branch_b, tools, route]
   output: visited=[branch_a]
   error: (none)
8. assistant/join#1
   node: assistant/join
   predecessors: assistant/branch_a#1, assistant/branch_b#1
   input: user_input="hello graph trace", route_count=2, visited=[start, prepare, route, branch_b, tools, route, branch_a]
   output: visited=[join]
   error: (none)
9. assistant/done#1
   node: assistant/done
   predecessors: assistant/join#1
   input: user_input="hello graph trace", route_count=2, visited=[start, prepare, route, branch_b, tools, route, branch_a, join]
   output: visited=[done]
   error: (none)

Trace edges
- assistant/start#1 -> assistant/prepare#1
- assistant/start#1 -> assistant/route#1
- assistant/prepare#1 -> assistant/branch_b#1
- assistant/route#1 -> assistant/tools#1
- assistant/tools#1 -> assistant/route#2
- assistant/route#2 -> assistant/branch_a#1
- assistant/branch_a#1 -> assistant/join#1
- assistant/branch_b#1 -> assistant/join#1
- assistant/join#1 -> assistant/done#1

Summary
- Repeated nodes: assistant/route x2
- Skipped nodes: assistant/branch_never
- Final step labels: assistant/done#1
```

## Related APIs

- `agent.WithExecutionTraceEnabled`
- `runner.Runner.Run`
- `event.Event.ExecutionTrace`
- `graph.NewStateGraph`
- `graph.StateGraph.AddConditionalEdges`
- `graph.StateGraph.AddJoinEdge`
- `graphagent.New`
