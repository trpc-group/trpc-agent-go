# Join Edge (Wait-All Fan-in) Example

This example demonstrates the `AddJoinEdge` feature in the `graph` package.

## What Is a “Join Edge”?

In a graph workflow, it is common to **fan out** work into multiple branches
(run multiple nodes in parallel), then **fan in** the results (continue only
after all branches are done).

`AddJoinEdge(fromNodes, to)` provides this **wait-all fan-in** behavior:

- `fromNodes`: the upstream node IDs that must all finish
- `to`: the downstream node ID that should run **once**, only after all
  `fromNodes` have completed

## Graph Shape

```
    start
    /  \
   a    b   (a and b run in parallel)
    \  /
    join    (runs only after both a and b finish)
```

## Run

```bash
cd examples/graph/join_edge
go run .
```

You can also change the simulated work time of each branch:

```bash
go run . -sleep-a 200ms -sleep-b 50ms
```

The output prints the final execution order stored in state. The relative order
between `a` and `b` may vary, but `join` always appears after both.
