# Evolution (Agent Self-Learning)

Evolution enables agents to **automatically extract reusable skills from
past executions** and load them on future tasks. It runs as an
asynchronous background loop — the main task path is never blocked.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Main Task Path                           │
│  Request ──▶ [skill_load] ──▶ Agent ──▶ Tool Calls ──▶ Result   │
└────────────────────────────────────┬────────────────────────────┘
                                     │ enqueue (transcript + outcome)
                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Background Learning Loop                     │
│                                                                 │
│  ┌──────────┐    ┌────────────┐    ┌───────────┐    ┌────────┐  │
│  │ Reviewer │──▶ │ Reconciler │──▶ │   Gates   │──▶ │Publish │  │
│  │ (LLM)    │    │(dedup/abs) │    │(A → B → C)│    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  Managed Skills   │ ◀── next task reads
                    │  (SKILL.md files) │
                    └───────────────────┘
```

**Key properties:**

- Fully asynchronous — no latency on the main path
- Deterministic reconciler prevents skill library bloat
- Quality gates (spec, safety, effectiveness, human approval) — all
  rule-based, zero LLM cost
- Immutable revision store with audit log and rollback capability

## Quick Start

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

// 1. Create a skill repository (reads SKILL.md files from disk).
repo, _ := skill.NewFSRepository("./managed_skills")

// 2. Create the evolution service with a reviewer model.
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./managed_skills"),
    evolution.WithSkillRepository(repo),
)
defer evoSvc.Close()

// 3. Wire skills into the agent.
agent := llmagent.New("my-agent",
    llmagent.WithModel(agentModel),
    llmagent.WithSkills(repo),
)

// 4. Wire evolution into the runner.
r := runner.NewRunner("app", agent,
    runner.WithEvolutionService(evoSvc),
)
defer r.Close()

// 5. Run tasks — skills are extracted in the background and
//    available to future runs via skill_load.
```

## Configuration Options

| Option                         | Description                                    |
| ------------------------------ | ---------------------------------------------- |
| `WithManagedSkillsDir(dir)`    | Directory for managed SKILL.md files           |
| `WithSkillRepository(repo)`    | Skill repo for reading existing skills         |
| `WithCandidateStore(store)`    | Immutable revision store (audit + rollback)    |
| `WithActivePointer(ptr)`       | Active revision pointer                        |
| `WithSpecGate(gate)`           | Schema/naming validation                       |
| `WithSafetyGate(gate)`         | Content safety scanner                         |
| `WithEffectivenessGate(gate)`  | Outcome-based effectiveness check              |
| `WithHumanGate(gate)`          | Human approval gate                            |
| `WithApprovalGateShadow(bool)` | Shadow mode — evaluate gates without enforcing |

## Quality Gates

Evolution passes each candidate skill revision through a pipeline of
gates before promoting it to the live library:

### SpecGate + SafetyGate (deterministic, zero LLM)

```go
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./managed_skills"),
    evolution.WithCandidateStore(evolution.NewFileCandidateStore("./revisions")),
    evolution.WithActivePointer(evolution.NewFileActivePointer("./revisions")),
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
)
```

- **SpecGate**: validates schema completeness, name stability, duplicate
  detection
- **SafetyGate**: scans for secret patterns (`sk-`, `AKIA`, JWT),
  dangerous shell commands (`rm -rf`), path traversal (`../../etc/passwd`)

### EffectivenessGate

```go
evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate())
```

Holds revisions extracted from failed sessions (score < 80 or
status=fail) in `pending_eval` state, preventing the agent from
learning incorrect procedures.

### HumanGate (optional human approval)

```go
// Hold all new skill creations for human review.
evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate())

// Or hold everything:
evolution.WithHumanGate(evolution.NewAlwaysHoldGate())
```

When configured, revisions that pass all automatic gates are held in
`pending_approval` state. An external system (CLI, API, webhook) then
approves or rejects them.

```go
// Query and approve pending revisions programmatically:
svc := evolution.NewApprovalService(store, pointer, publisher)
pending, _ := svc.ListPending(ctx, evolution.ListPendingOpts{})
svc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   true,
    Reviewer:   "alice@example.com",
})
```

## Revision Lifecycle

Each skill mutation is stored as an immutable revision with a clear
status progression:

```
pending ──→ [SpecGate fail]      ──→ rejected
        ──→ [SafetyGate fail]    ──→ rejected
        ──→ [EffectivenessGate]  ──→ pending_eval
        ──→ [HumanGate hold]     ──→ pending_approval
        ──→ [all pass]           ──→ active

pending_approval ──→ [approve] ──→ active
                 ──→ [reject]  ──→ rejected

active ──→ [superseded] ──→ archived (rollback possible)
```

## Reconciler

The reconciler runs deterministic deduplication rules on the reviewer's
output before the gates:

1. **Strict-superset rewrite**: if a new skill name is a task-variant
   superset of an existing skill (e.g. "Weather 5 Cities" vs "Weather
   Multi-City"), convert the create to an update
2. **Intra-batch dedup**: if the reviewer produces multiple skills with
   the same logical name, keep only the last
3. **Quantified-sibling absorption**: count-specific names (`3 Cities`)
   are rewritten to generic-parent form (`Multi-City`)

This ensures the skill library converges rather than growing unbounded.

## Providing Outcomes

For the effectiveness gate to work, attach an `Outcome` to learning
jobs:

```go
runner.WithEvolutionOutcomeHook(func(ctx context.Context, result *runner.Result) *evolution.Outcome {
    return &evolution.Outcome{
        Status: evolution.OutcomeSuccess,
        Score:  &score, // 0.0–1.0
        Notes:  "all assertions passed",
    }
})
```

## Example

See [`examples/evolution/`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evolution)
for a complete working example with managed skills, quality gates, and
human approval.
