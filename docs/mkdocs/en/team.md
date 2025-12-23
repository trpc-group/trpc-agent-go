# Team

The `team` package is a high-level way to run multiple Agents together.

It gives you two simple collaboration styles:

- **Coordinator Team**: one Agent stays in charge, calls member Agents as
  tools, then replies to the user.
- **Swarm**: no coordinator loop; the current Agent can hand off to another
  Agent via `transfer_to_agent`.

In the examples below, `LLMAgent` is an Agent implementation backed by a
Large Language Model (LLM).

## Why do you need a Team?

An Agent is good at one role. Real applications often need multiple roles,
for example:

- researching a problem
- writing code
- reviewing for mistakes

A Team lets you combine these roles with a small, clear
Application Programming Interface (API).

## Coordinator Team vs Swarm

### Coordinator Team

- **Best for**: combining multiple outputs into one final answer (for example,
  consensus, summarization, "review then revise").
- **How it works**: the coordinator calls members as tools (using AgentTool).
  The coordinator can call multiple members in one run.

```mermaid
flowchart LR
    U[User] --> T[Coordinator]
    T -->|tool call| A[Member A]
    T -->|tool call| B[Member B]
    T -->|tool call| C[Member C]
    T --> U
```

### Swarm

- **Best for**: “handoff chains”, where each Agent decides who should act
  next.
- **How it works**: an entry Agent starts, then Agents transfer control to the
  next Agent using `transfer_to_agent`. The final Agent replies to the user.

```mermaid
sequenceDiagram
    participant U as User
    participant A as Agent A
    participant B as Agent B
    participant C as Agent C

    U->>A: prompt
    A->>B: transfer_to_agent
    B->>C: transfer_to_agent
    C->>U: final answer
```

## Quickstart: Coordinator Team

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/team"
)

modelInstance := openai.New("deepseek-chat")

coder := llmagent.New(
    "coder",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("Write Go code."),
)

reviewer := llmagent.New(
    "reviewer",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("Review for correctness."),
)

coordinator := llmagent.New(
    "team",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(
        "You are the coordinator. Call member agents as tools, "+
            "then produce the final answer.",
    ),
)

tm, err := team.New(
    coordinator,
    []agent.Agent{coder, reviewer},
    team.WithDescription("A tiny coordinator team"),
)
if err != nil {
    panic(err)
}

r := runner.NewRunner("app", tm)
_ = r
```

Notes:

- The Team name is derived from the coordinator name (here both are `"team"`;
  `team.New` reuses `coordinator.Info().Name`), so the same session history
  and events don't end up with two different Agent author names.
- The coordinator must support dynamic ToolSets (LLMAgent does).
- If you want to stream member output in the parent transcript, enable
  `team.WithMemberToolStreamInner(true)` and use streaming for both the
  coordinator and members.

## Quickstart: Swarm

```go
tm, err := team.NewSwarm(
    "team",
    "researcher", // entrypoint member name
    []agent.Agent{coder, researcher, reviewer},
)
if err != nil {
    panic(err)
}
```

Notes:

- `entryName` is the Swarm entrypoint: a run starts from this member. It must
  exist in `members` (it must match the member's `Info().Name`).
- Members must support `SetSubAgents` (LLMAgent does). This is required so
  members can discover and transfer to each other.

## Swarm Guardrails

Swarm-style handoffs can loop if Agents keep transferring back and forth.
`team.SwarmConfig` provides optional limits (a zero value means "no limit" or
"disabled"):

- `MaxHandoffs`: maximum transfers in one run (across the whole Team; each
  `transfer_to_agent` counts as 1)
- `NodeTimeout`: maximum runtime for the target Agent after a transfer (only
  applies to transfer targets)
- `RepetitiveHandoffWindow` + `RepetitiveHandoffMinUnique`: loop detection
  (sliding window)

```go
import "time"

tm, err := team.NewSwarm(
    "team",
    "researcher",
    members,
    team.WithSwarmConfig(team.SwarmConfig{
        MaxHandoffs:                20,
        NodeTimeout:                300 * time.Second,
        RepetitiveHandoffWindow:    8,
        RepetitiveHandoffMinUnique: 3,
    }),
)
```

Defaults come from `team.DefaultSwarmConfig()`: `MaxHandoffs=20`,
`RepetitiveHandoffWindow=8`, `RepetitiveHandoffMinUnique=3`, and
`NodeTimeout=0` (no timeout).

Loop detection meaning: with a window size N and a minimum unique count M, if
the last N transfer targets (toAgent) contain fewer than M unique Agents, the
transfer is rejected. This is a coarse but efficient heuristic: it only looks
at recent target Agent names, not the full transfer path.

Example: if `RepetitiveHandoffWindow=8` and `RepetitiveHandoffMinUnique=3`,
and transfers keep bouncing between A and B (A => B, B => A, ...), then the
last 8 transfer targets contain only 2 unique Agents, which is less than 3,
so the transfer is rejected.

Note: if you set M to 2, the A↔B "ping-pong" will NOT be blocked (because the
unique count is exactly 2, not fewer than 2). To cover this case, set M to 3
or higher.

## Example

See `examples/team` for a runnable demo of both modes.

## Design Notes

- Coordinator teams expose members as tools by installing AgentTool wrappers on
  the coordinator.
- Swarm teams wire members together via `SetSubAgents` and rely on
  `transfer_to_agent` for handoffs.
- `team.SwarmConfig` is enforced by a transfer controller stored in
  `RunOptions.RuntimeState` during a run.
