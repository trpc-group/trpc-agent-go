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
│  │ (LLM)    │    │(dedup/abs) │    │           │    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  skills/evolution/ │ ◀── next task reads
                    │  (SKILL.md files)  │
                    └───────────────────┘
```

**Key properties:**

- Fully asynchronous — no latency on the main path
- Deterministic reconciler prevents skill library bloat
- Quality gates (spec, safety, effectiveness, human approval) — all
  rule-based, zero LLM cost
- Immutable revision store with audit log and rollback capability
- Write isolation — evolution cannot modify bundled or user-authored skills

## Directory Layout

Evolution uses the following layout under the runtime's `state_dir`:

```
<state_dir>/
  skills/
    bundled/              ← built-in skills (read-only)
    local/                ← user-authored skills
    evolution/            ← auto-learned skills (evolution writes here)
      market-analysis/SKILL.md
  evolution/
    revisions/            ← immutable revision snapshots + audit logs
      market-analysis/
        revisions/<id>/meta.json
        active.txt
        audit.log
```

- `skills/evolution/` is written by the evolution publisher; the agent
  reads these via `skill_load`
- `evolution/revisions/` stores version history and audit logs for
  diff, rollback, and approval workflows

## Quick Start

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

// 1. Create a skill repository (reads SKILL.md files from disk).
repo, _ := skill.NewFSRepository("./skills")

// 2. Create the evolution service with full quality gates.
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir("./skills/evolution"),
    evolution.WithSkillRepository(repo),
    evolution.WithCandidateStore(evolution.NewFileCandidateStore("./evolution/revisions")),
    evolution.WithActivePointer(evolution.NewFileActivePointer("./evolution/revisions")),
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
    evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),
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

## Runtime Configuration (OpenClaw YAML)

Configure evolution behavior in `openclaw.yaml`:

```yaml
evolution:
  # Human approval gate:
  #   "always" — hold all revisions for human approval
  #   "create" — hold new skills only, auto-pass updates
  #   ""       — disabled (default), revisions auto-promote
  human_gate: ""
```

Evolution is **automatically enabled** when `state_dir` is set and the
model is not mock — no additional configuration needed.

## Configuration Options (Programmatic)

| Option                         | Description                                    |
| ------------------------------ | ---------------------------------------------- |
| `WithManagedSkillsDir(dir)`    | Directory where evolution writes SKILL.md      |
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

- **SpecGate**: validates schema completeness, name stability, duplicate
  detection
- **SafetyGate**: scans for secret patterns (`sk-`, `AKIA`, JWT),
  dangerous shell commands (`rm -rf`), path traversal (`../../etc/passwd`)

### EffectivenessGate

Holds revisions extracted from failed sessions (score < 80 or
status=fail) in `pending_eval` state, preventing the agent from
learning incorrect procedures.

### HumanGate (optional human approval)

When configured, revisions that pass all automatic gates are held in
`pending_approval` state. Use the CLI to approve or reject:

```bash
# List pending revisions
openclaw evolution pending --dir <state_dir>/evolution/revisions

# View revision details
openclaw evolution diff <revision-id> --dir <state_dir>/evolution/revisions

# Approve (publish + promote)
openclaw evolution approve <revision-id> --dir <state_dir>/evolution/revisions

# Reject
openclaw evolution reject <revision-id> --dir <state_dir>/evolution/revisions --comment "reason"

# View audit trail
openclaw evolution audit --dir <state_dir>/evolution/revisions
```

### Write Isolation

Evolution **can only modify skills it created** (under `skills/evolution/`).
Updates targeting bundled or user-authored skills are silently skipped
with a warning log, ensuring hand-written and built-in skills are never
accidentally overwritten.

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
   the same logical name or shape, keep only the first
3. **Quantified-sibling absorption**: count-specific names (`3 Cities`)
   are rewritten to generic-parent form (`Multi-City`)
4. **Word-overlap merge**: if a new skill name shares ≥50% significant
   words with an existing skill (e.g. "Geopolitical Market Snapshot" vs
   "Geopolitical Market Analysis"), convert the create to an update

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
for a complete runnable example with managed skills, quality gates,
and human approval.
