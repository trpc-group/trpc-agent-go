# Evolution (Agent Self-Learning)

## Overview

Evolution is the self-learning system in the tRPC-Agent-Go framework. It enables agents to **automatically extract reusable skills from past executions** and load them on future tasks. The entire process runs as an asynchronous background loop — the main task path is never blocked.

### Purpose

Evolution accumulates and reuses the agent's "operational experience". Skills are unscoped by default (`SkillScopeNone`); app-level or user-level isolation can be enabled explicitly. When an agent completes a task, the background learning loop checks the review policy (default: ≥4 tool calls, user correction, or recovered error) before invoking the Reviewer. If the delta is worth reviewing, the Reviewer analyzes the conversation transcript and extracts reusable workflows as structured SKILL.md files. On subsequent similar tasks, the agent loads the matching skill via `skill_load` and follows the proven steps directly, avoiding repeated trial-and-error.

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
                                     │ enqueue + review policy
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
| **ReviewPolicy** | Decide whether to review (default: ≥4 tool calls) | `DefaultReviewPolicy` |
| **Reviewer** | Extract skill spec from transcript (JSON) | `LLMReviewer` (gpt-4o-mini) |
| **Reconciler** | Deterministic dedup/absorb/merge (4 rules) | Pure string rules |
| **SpecGate** | Validate spec schema, naming, duplicates | Deterministic |
| **SafetyGate** | Scan for secrets, dangerous commands, path traversal | Deterministic |
| **EffectivenessGate** | Block revisions from failed sessions | Deterministic |
| **HumanGate** | Optional human approval | `NewAlwaysHoldGate` / `NewCreateOnlyHoldGate` |
| **Publisher** | Write SKILL.md to disk | File-based publisher |

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
    // WithEvolutionService borrows evoSvc; keep the explicit evoSvc.Close.

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

## Offline Reflective Optimization

The asynchronous reviewer learns one candidate from one completed session.
For skills that have a repeatable benchmark, `evolution/optimization` adds a
separate pure-Go search loop inspired by GEPA. It does not require DSPy,
Python, or a companion process.

The optimizer:

1. evaluates the seed skill on a validation split;
2. evaluates a Pareto-selected parent on a feedback minibatch and sends the
   score, output, evaluator feedback, and redacted trace to a reflection model;
3. changes exactly one `SkillSpec` component and accepts the child only when it
   strictly improves the paired minibatch;
4. tracks per-case validation winners and samples parents by instance-level
   Pareto coverage;
5. compares the final candidate with the seed on an unseen holdout split; and
6. optionally submits the candidate to the existing revision store. External
   submissions initially stop at `pending_approval` and never update the live
   skill directly. By default they remain there until reviewed. If the service
   enables `WithApprovalTimeout`, the existing auto-expiration sweeper may
   later promote and publish a stale revision without human approval.

The application supplies both a task-specific evaluator and a reflection
`model.Model`. Configure the reflection model with the same provider adapters
used by other agents; it may be the task model or a separate reviewer model.
The evaluator is the only new domain-specific integration:

```go
type benchmarkEvaluator struct {
    // runner/sandbox/test harness dependencies
}

func (e *benchmarkEvaluator) Evaluate(
    ctx context.Context,
    candidate *evolution.SkillSpec,
    cases []optimization.Case,
    seed int64,
) ([]optimization.Evaluation, error) {
    // Load candidate in an isolated repository, run every case, and return
    // one normalized [0,1] score plus actionable feedback/trace per case.
    return evaluations, nil
}
```

Revision submission is a separate optional capability from asynchronous session
learning. The default service implements `evolution.RevisionSubmitter`, while a
custom `evolution.Service` may omit it. Resolve that capability once during
application wiring so a configuration error is found before an optimization
run starts:

```go
revisionSubmitter, ok := evoSvc.(evolution.RevisionSubmitter)
if !ok {
    return fmt.Errorf("evolution service does not support revision submission")
}

optimizer, err := optimization.New(
    reflectionModel,
    evaluator,
    optimization.WithMaxIterations(10),
    optimization.WithReflectionBatchSize(3),
    optimization.WithRandomSeed(7),
    optimization.WithStoreDir("./evolution/experiments"),
    optimization.WithRevisionSubmitter(revisionSubmitter),
)
if err != nil {
    return err
}

result, err := optimizer.Optimize(ctx, optimization.Request{
    Seed:             baselineSpec,
    ParentRevisionID: activeRevisionID,
    Submit:           true,
    Dataset: optimization.Dataset{
        ID:         "managed-skill-regression",
        Version:    "v1",
        Feedback:   feedbackCases,
        Validation: validationCases,
        Holdout:    holdoutCases,
    },
})
if err != nil {
    return err
}
fmt.Printf("selected skill %q; validation=%.3f holdout=%.3f; promote=%t (%s)\n",
    result.Spec.Name,
    result.CandidateValidation.Score,
    result.CandidateHoldout.Score,
    result.PromotionEligible,
    result.PromotionReason,
)
```

The optimizer borrows the submitter. The application still owns and closes
`evoSvc`.

Case IDs must be unique across splits. Scores must be finite and normalized to
`[0,1]`. `PromotionEligible` and `PromotionReason` are populated even when
`Submit` is false, so callers do not need to duplicate the holdout threshold
and critical-case policy. Submission requires at least ten cases in each split.
Keep holdout cases hidden from the search, redact secrets from feedback and
traces, and run candidate agents without production credentials or
side-effecting tools.

## Review Policy

Evolution decides whether to review after each task completion. The built-in `DefaultReviewPolicy` triggers when any of these conditions hold:

| Condition | Rationale |
|-----------|-----------|
| `ToolCallCount ≥ 4` | Multi-step tasks have extraction value |
| `HasUserCorrection` | User corrected the agent → worth recording pitfall |
| `HasRecoveredError` | Agent recovered from error → worth recording experience |

Tune the built-in policy:

```go
evolution.WithReviewPolicy(evolution.DefaultReviewPolicy{
    MinToolCalls: 6, // more conservative than the default 4
})
```

Custom policy implementations can call centralized services or use tenant-specific rules. Policy errors do not advance the review cursor, so the same delta can be retried later.

```go
type centralPolicy struct {
    client *ReviewDecisionClient
}

func (p *centralPolicy) ShouldReview(
    ctx context.Context,
    input *evolution.ReviewPolicyInput,
) (bool, error) {
    if input == nil || input.ReviewContext == nil {
        return false, nil
    }
    return p.client.ShouldReview(ctx, ReviewRequest{
        AppName:       input.AppName,
        UserID:        input.UserID,
        SessionID:     input.SessionID,
        ToolCallCount: input.ReviewContext.ToolCallCount,
        Outcome:       input.Outcome,
    })
}

evolution.WithReviewPolicy(&centralPolicy{client: client})
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
- Normalized session score < 0.8 → revision held in `pending_eval`

Requires an Outcome to be attached to the learning job:

```go
evoSvc.EnqueueLearningJob(ctx, evolution.LearningJob{
    Session: sess,
    Outcome: &evolution.Outcome{
        Status: evolution.OutcomeSuccess,
        Score:  floatPtr(0.95), // normalized to 0-1
        Notes:  "all assertions passed",
    },
})
```

### HumanGate (Optional)

Holds create/update/delete revisions after all automatic gates pass:

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
    Comment:    "looks good",
})
```

The decision is stored on `HumanReport` (`Approved`, `Reviewer`, `Comment`,
`DecidedAt`) and appended to the audit log. Delete revisions held by a
`HumanGate` do not mutate the live skill until approval; approving the delete
calls `Publisher.DeleteSkill` and clears the active pointer.

#### Auto-expire

To prevent revisions sitting in `pending_approval` forever, configure an
expiration timeout. Revisions older than the timeout are auto-promoted to
`active` (Reviewer = `auto-expire`, recorded in the audit log):

```go
evoSvc := evolution.NewService(reviewerModel,
    evolution.WithManagedSkillsDir(skillsDir),
    evolution.WithSkillRepository(repo),
    evolution.WithCandidateStore(evolution.NewFileCandidateStore(revisionsDir)),
    evolution.WithActivePointer(evolution.NewFileActivePointer(revisionsDir)),
    evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate()),
    evolution.WithApprovalTimeout(72*time.Hour), // auto-promote after 3 days
    // Optional: override the sweep period (default min(timeout/4, 1h))
    // evolution.WithApprovalSweepInterval(15*time.Minute),
)
```

The sweeper runs as a background goroutine inside the worker; it stops on
`Service.Close()`. Setting the timeout to `0` (default) disables the sweeper.

In `openclaw` YAML, use duration strings:

```yaml
evolution:
  enabled: true
  human_gate: "create"
  approval_timeout: "72h"
  # Optional: defaults to min(approval_timeout/4, 1h)
  approval_sweep_interval: "15m"
```

#### Rollback

Restore the previous active revision when a new one regresses quality. The
current active revision is demoted to `archived` and the chosen archived
revision is promoted back to `active`. The publisher is updated immediately
so agents pick up the restored skill on their next read:

```go
res, err := approvalSvc.Rollback(ctx, "weather-monitor", evolution.RollbackOpts{
    Reviewer: "alice@example.com",
    Comment:  "regressed quality",
    // TargetRevisionID: "20260601T120000.000-abc123", // optional: pick a specific archived revision
})
fmt.Printf("rolled back %s → %s\n", res.PreviousActiveID, res.RestoredID)
```

When `TargetRevisionID` is empty, the latest archived revision in the
revision store's ordering wins.
`ErrNoArchivedRevision` is returned when no archived revision is available.

The `openclaw` CLI exposes the same workflow:

```bash
openclaw evolution rollback <skill-id> --dir <revisions-dir> [--revision <rev-id>] [--reviewer <id>] [--comment <text>]
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

For approved delete revisions, the delete revision is recorded and the active
pointer is cleared so no previous revision remains visible.

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
| `WithSkillRepositoryProvider(p)` | Resolve the skill repository per `SkillScope` (multi-tenant isolation, see below) | nil |
| `WithSkillScopeMode(mode)` | Isolation granularity: `SkillScopeApp` (share per app) / `SkillScopeUser` (isolate per app+user) | `SkillScopeNone` (no isolation) |
| `WithReviewPolicy(p)` | Review trigger policy | `DefaultReviewPolicy` (≥4 tool calls) |
| `WithCandidateStore(store)` | Immutable revision store | nil (no tracking) |
| `WithActivePointer(ptr)` | Active revision pointer | nil |
| `WithSpecGate(gate)` | Schema/naming validation | nil |
| `WithSafetyGate(gate)` | Safety scanner | nil |
| `WithEffectivenessGate(gate)` | Effectiveness check | nil |
| `WithHumanGate(gate)` | Human approval | nil (disabled) |
| `WithApprovalGateShadow(bool)` | Shadow mode — evaluate but don't enforce | false |
| `WithApprovalTimeout(d)` | Auto-promote `pending_approval` revisions older than `d` | 0 (disabled) |
| `WithApprovalSweepInterval(d)` | Sweep period for the auto-expire goroutine | min(timeout/4, 1h) |
| `WithWorkerNum(n)` | Async worker count | 1 |
| `WithQueueSize(n)` | Per-worker job queue buffer | 10 |
| `WithExistingSkillBodyMaxChars(n)` | Body excerpt length for reviewer context | 600 |
| `WithReviewerOptions(...)` | LLM reviewer options (temperature etc.) | - |
| `WithReviewer(r)` | Custom Reviewer implementation | LLMReviewer |
| `WithPublisher(p)` | Custom Publisher implementation | File-based publisher |

## Write Isolation

When `ManagedSkillsDir` is configured, evolution enforces write isolation:

- **Create**: always allowed — new skill written to ManagedSkillsDir
- **Update**: only allowed for skills within ManagedSkillsDir; updates targeting bundled or user-authored skills are silently skipped (warning logged)
- **Delete**: same rules as update

This ensures evolution never accidentally modifies hand-written or built-in skills.

## Multi-Tenant Isolation

By default (`SkillScopeNone`) all sessions share one skill library and revision
directory. In multi-app / multi-user deployments, evolution can isolate skills
per **app** or per **app+user** so that one tenant's learned skills never
pollute another's.

### Isolation granularity (`SkillScopeMode`)

| Mode | Description | File layout |
|------|-------------|-------------|
| `SkillScopeNone` (default) | No isolation, globally shared | `<root>/...` |
| `SkillScopeApp` | Shared per app across that app's users | `<root>/apps/<app>/...` |
| `SkillScopeUser` | Isolated per app+user | `<root>/users/<app>/<user>/...` |

The `SkillScope` is derived from the session's `AppName` / `UserID` (or set
explicitly via `LearningJob.Scope`). App/user identifiers are sanitized for
filesystem safety: illegal or overly long identifiers are replaced with a
stable hash segment (`id-<hash>`), preventing path traversal while staying
idempotent.

### Wiring

Repository resolution goes through `skill.RepositoryProvider`, which returns
the `skill.Repository` for a given `SkillScope`:

```go
// Resolve the skill repository per scope (the implementation decides whether
// to switch directories per app or per user).
provider := skill.RepositoryProviderFunc(
    func(ctx context.Context, scope skill.SkillScope) (skill.Repository, error) {
        roots, err := resolveScopedRoots(scope) // your directory mapping
        if err != nil {
            return nil, err
        }
        return skill.NewFSRepository(roots...)
    },
)

evoSvc := evolution.NewService(reviewModel,
    evolution.WithManagedSkillsDir(managedDir),
    evolution.WithSkillRepositoryProvider(provider),
    evolution.WithSkillScopeMode(skill.SkillScopeUser), // isolate per app+user
    // ...
)
```

On the agent side, use the matching `llmagent.WithSkillRepositoryProvider` /
`llmagent.WithSkillScopeMode` so the skills injected into the prompt and the
skills evolution writes share the same scope.

> Note: the `Publisher`, `CandidateStore`, and `ActivePointer` interfaces stay
> simple (no `SkillScope` parameter); the worker routes the file-backed
> implementations into the corresponding sub-directory based on the resolved
> scope. The default file implementations therefore gain multi-tenant
> isolation without any change.

## Metrics

Read gate activity metrics via the `ApprovalGateMetricsProvider` interface (a
read-only snapshot that does not expose the internal worker):

```go
if mp, ok := evoSvc.(evolution.ApprovalGateMetricsProvider); ok {
    m := mp.ApprovalGateMetrics()
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
- Custom review policy configuration
