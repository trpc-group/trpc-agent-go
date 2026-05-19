# Evolution (Agent Self-Learning)

## Overview

Evolution is the self-learning system in the tRPC-Agent-Go framework. It enables agents to **automatically extract reusable skills from past executions** and load them on future tasks. The entire process runs as an asynchronous background loop — the main task path is never blocked.

### Purpose

Evolution accumulates and reuses the agent's "operational experience" at the application level. When an agent completes a multi-step task (≥4 tool calls by default), a background Reviewer analyzes the conversation transcript and extracts reusable workflows as structured SKILL.md files. On subsequent similar tasks, the agent loads the matching skill via `skill_load` and follows the proven steps directly, avoiding repeated trial-and-error.

It excels at capturing: stable multi-step workflows, tool-calling best practices, common pitfalls and avoidance strategies, domain-specific operational procedures.

### Key Benefits

- **Efficiency**: similar tasks that initially require multi-round exploration complete in one pass after skill extraction (benchmark: 17-33% token savings)
- **Disaster suppression**: skills provide clear steps, eliminating random infinite loops on certain tasks (up to 94.6% savings on worst-case runs)
- **Experience reuse**: pitfalls learned once persist permanently, independent of session context
- **Quality control**: quality gates ensure only qualified skills go live; write isolation protects existing assets

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Main Task Path                           │
│  Request ──▶ [skill_load] ──▶ Agent ──▶ Tool Calls ──▶ Result   │
└────────────────────────────────────┬────────────────────────────┘
                                     │ enqueue (≥4 tool calls)
                                     ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Background Learning Loop (async)               │
│                                                                 │
│  ┌──────────┐    ┌────────────┐    ┌───────────┐    ┌────────┐  │
│  │ Reviewer │──▶ │ Reconciler │──▶ │   Gates   │──▶ │Publish │  │
│  │ (LLM)    │    │(dedup/merge)│   │ Spec/Safe │    │        │  │
│  │          │    │            │    │ Effect/   │    │        │  │
│  │          │    │            │    │ Human     │    │        │  │
│  └──────────┘    └────────────┘    └───────────┘    └───┬────┘  │
└─────────────────────────────────────────────────────────┼───────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌───────────────────┐
                    │  Managed Skills   │ ◀── next task loads
                    │  (SKILL.md files) │     via skill_load
                    └───────────────────┘
```

**Pipeline stages:**

| Stage | Responsibility | Implementation |
|-------|----------------|----------------|
| **Policy** | Decide whether to review (default: ≥4 tool calls) | `DefaultPolicy` |
| **Reviewer** | Extract skill spec from transcript (JSON) | `LLMReviewer` (gpt-4o-mini) |
| **Reconciler** | Deterministic dedup/absorb/merge (4 rules) | Pure string rules |
| **SpecGate** | Validate spec schema, naming, duplicates | Deterministic |
| **SafetyGate** | Scan for secrets, dangerous commands, path traversal | Deterministic |
| **EffectivenessGate** | Block revisions from failed sessions | Deterministic |
| **HumanGate** | Optional human approval | `AlwaysHoldGate` / `CreateOnlyHoldGate` |
| **Publisher** | Write SKILL.md to disk | `FilePublisher` |

## Quick Start

### Minimal Setup

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/evolution"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

func main() {
    agentModel := openai.New("gpt-4o")
    reviewerModel := openai.New("gpt-4o-mini") // small model suffices for reviewer

    // 1. Create skill repository
    //    repo is the shared dependency between agent and evolution:
    //    - agent reads skill overviews and loads skills via skill_load
    //    - evolution's reviewer reads existing skills for dedup decisions
    //    - after evolution publishes, repo.Refresh() makes new skills
    //      immediately visible to both agent and reviewer
    repo, _ := skill.NewFSRepository("./skills")

    // 2. Create evolution service
    //    ManagedSkillsDir: the directory where evolution writes SKILL.md files
    //    SkillRepository:  pass the same repo shared with the agent so that:
    //      a) the reviewer sees all existing skills (bundled/local/evolution)
    //      b) a single Refresh after publish updates both agent and reviewer
    evoSvc := evolution.NewService(reviewerModel,
        evolution.WithManagedSkillsDir("./skills"),
        evolution.WithSkillRepository(repo),
    )
    defer evoSvc.Close()

    // 3. Create agent with skills (same repo instance)
    agent := llmagent.New("my-agent",
        llmagent.WithModel(agentModel),
        llmagent.WithSkills(repo),
    )

    // 4. Create runner with evolution
    r := runner.NewRunner("app", agent,
        runner.WithEvolutionService(evoSvc),
    )
    defer r.Close()

    // 5. Run tasks normally — skills are extracted in background
    //    and loaded via skill_load on subsequent matching tasks.
}
```

### Production Setup (Recommended)

```go
skillsDir := "./skills/evolution"
revisionsDir := "./evolution/revisions"

evoSvc := evolution.NewService(reviewerModel,
    // Core
    evolution.WithManagedSkillsDir(skillsDir),
    evolution.WithSkillRepository(repo),

    // Immutable revision store (audit + rollback)
    evolution.WithCandidateStore(evolution.NewFileCandidateStore(revisionsDir)),
    evolution.WithActivePointer(evolution.NewFileActivePointer(revisionsDir)),

    // Quality gate chain
    evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
    evolution.WithSafetyGate(evolution.NewDefaultSafetyGate()),
    evolution.WithEffectivenessGate(evolution.NewOutcomeBasedEffectivenessGate()),

    // Optional: human approval
    evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate()),
)
```

## Trigger Policy

Evolution decides whether to review after each task completion. The default `DefaultPolicy` triggers when any of these conditions hold:

| Condition | Rationale |
|-----------|-----------|
| `ToolCallCount ≥ 4` | Multi-step tasks have extraction value |
| `HasUserCorrection` | User corrected the agent → worth recording pitfall |
| `HasRecoveredError` | Agent recovered from error → worth recording experience |

Custom policy:

```go
type myPolicy struct{}
func (myPolicy) ShouldReview(ctx *evolution.ReviewContext) bool {
    return ctx.ToolCallCount >= 6 // more conservative
}

evolution.WithPolicy(myPolicy{})
```

## Quality Gates

### SpecGate

Deterministic schema/naming validation:

- **Schema**: name / description / when_to_use / steps must be non-empty
- **Naming**: no numeric counts (e.g. "3 Cities"), no excessive length
- **Dedup**: rejects exact-name duplicates of existing skills

### SafetyGate

Deterministic content safety scan:

- **Secrets**: `sk-*`, `AKIA*`, JWT tokens, private key markers
- **Dangerous commands**: `rm -rf /`, `chmod 777`, `> /dev/sda`
- **Path traversal**: `../../etc/passwd`, `/root/.ssh/`

### EffectivenessGate

Outcome-based quality check:

- Session result `fail` or `agent_error` → revision rejected
- Session score < 80 → revision held in `pending_eval`

Requires an Outcome to be attached to the learning job:

```go
evoSvc.EnqueueLearningJob(ctx, evolution.LearningJob{
    Session: sess,
    Outcome: &evolution.Outcome{
        Status: evolution.OutcomeSuccess,
        Score:  floatPtr(95.0), // 0-100
        Notes:  "all assertions passed",
    },
})
```

### HumanGate (Optional)

Holds revisions after all automatic gates pass:

```go
// Hold all revisions
evolution.WithHumanGate(evolution.NewAlwaysHoldGate())

// Hold only new creations, auto-pass updates
evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate())
```

Approve/reject via `ApprovalService`:

```go
approvalSvc := evolution.NewApprovalService(store, pointer, publisher)
pending, _ := approvalSvc.ListPending(ctx, evolution.ListPendingOpts{})
approvalSvc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   true,
    Reviewer:   "alice@example.com",
})
```

### Custom Gates

Implement the interface to plug in custom logic:

```go
type HumanGate interface {
    ShouldHold(ctx context.Context, rev *Revision, outcome *Outcome) (bool, error)
}
```

## Reconciler (Deterministic Dedup)

Runs before gates on the reviewer's raw output:

| Rule | Trigger | Action |
|------|---------|--------|
| **Rule 1: Strict-superset** | New name is a task-variant superset of existing (e.g. "Weather - 5 Cities" vs "Weather Multi-City") | create → update |
| **Rule 2: Intra-batch dedup** | Reviewer outputs multiple skills with same name/shape in one batch | Keep first, drop rest |
| **Rule 3: Quantified-sibling** | Count-specific name (`3 Cities`) maps to existing generic parent (`Multi-City`) | create → update |
| **Rule 4: Word-overlap** | New name shares ≥50% significant words with existing (e.g. "Market Snapshot" vs "Market Analysis") | create → update |

All rules are deterministic (pure string operations), zero LLM cost.

## Revision Lifecycle

Each skill mutation is stored as an immutable revision:

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

On-disk structure:

```
revisions/
  <skill-id>/
    revisions/
      <revision-id>/
        meta.json          ← full Revision snapshot
    active.txt             ← current active revision ID
    audit.log              ← append-only audit log (JSON lines)
```

## Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithManagedSkillsDir(dir)` | Directory where evolution **writes** SKILL.md files (Publisher target) | Required |
| `WithSkillRepository(repo)` | Skill repo for **reading** existing skills for dedup; should be the same instance shared with the agent | Required |
| `WithPolicy(p)` | Trigger policy | `DefaultPolicy` (≥4 tool calls) |
| `WithCandidateStore(store)` | Immutable revision store | nil (no tracking) |
| `WithActivePointer(ptr)` | Active revision pointer | nil |
| `WithSpecGate(gate)` | Schema/naming validation | nil |
| `WithSafetyGate(gate)` | Safety scanner | nil |
| `WithEffectivenessGate(gate)` | Effectiveness check | nil |
| `WithHumanGate(gate)` | Human approval | nil (disabled) |
| `WithApprovalGateShadow(bool)` | Shadow mode — evaluate but don't enforce | false |
| `WithWorkerNum(n)` | Async worker count | 1 |
| `WithQueueSize(n)` | Per-worker job queue buffer | 16 |
| `WithExistingSkillBodyMaxChars(n)` | Body excerpt length for reviewer context | 600 |
| `WithReviewerOptions(...)` | LLM reviewer options (temperature etc.) | - |
| `WithReviewer(r)` | Custom Reviewer implementation | LLMReviewer |
| `WithPublisher(p)` | Custom Publisher implementation | FilePublisher |

## Write Isolation

When `ManagedSkillsDir` is configured, evolution enforces write isolation:

- **Create**: always allowed — new skill written to ManagedSkillsDir
- **Update**: only allowed for skills within ManagedSkillsDir; updates targeting bundled or user-authored skills are silently skipped (warning logged)
- **Delete**: same rules as update

This ensures evolution never accidentally modifies hand-written or built-in skills.

## Metrics

Read gate activity metrics via the `ServiceWithWorker` interface:

```go
if svcW, ok := evoSvc.(evolution.ServiceWithWorker); ok {
    m := svcW.Worker().ApprovalGateMetricsJSON()
    fmt.Printf("Candidates seen:      %d\n", m.CandidatesSeen)
    fmt.Printf("Revisions promoted:   %d\n", m.RevisionsPromoted)
    fmt.Printf("Spec-gate rejected:   %d\n", m.SpecGateRejected)
    fmt.Printf("Safety-gate rejected: %d\n", m.SafetyGateRejected)
    fmt.Printf("Effect-gate held:     %d\n", m.EffectivenessGateRejected)
    fmt.Printf("Human-gate held:      %d\n", m.HumanGateHeld)
}
```

## Example

See [`examples/evolution/`](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evolution)
for a complete runnable example demonstrating:

- Automatic skill extraction across multiple task rounds
- Cold-start to warm-start progression
- Quality gate metrics
- Custom policy configuration
