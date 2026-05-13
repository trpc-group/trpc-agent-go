# Evolution Example

Demonstrates the **Evolution** (Agent Self-Learning) feature of
trpc-agent-go with a real LLM and live skill extraction.

## What It Does

1. Runs multiple rounds of similar tasks (city info lookup)
2. After each task, the background reviewer extracts a reusable skill
3. On subsequent rounds, the agent loads the skill via `skill_load`
4. You can observe: skill creation, gate metrics, and warm-start behavior

## Running

```bash
export OPENAI_API_KEY="sk-..."
cd examples/evolution
go run main.go
```

Options:

```bash
go run main.go -model gpt-4o-mini -rounds 5
go run main.go -clean  # reset skills and start fresh
```

## Expected Output

```
============================================================
Round 1/3
Task: Look up the population and country of Tokyo, then summarize...
Skills available: 0
------------------------------------------------------------

Response: Tokyo has a population of 13.96 million and is in Japan.
Tool calls: 1 | Time: 2.3s
Waiting for background skill extraction... done.

============================================================
Round 2/3
Task: Look up the population and country of Paris, then summarize...
Skills available: 1 [City Info Lookup]
------------------------------------------------------------

Response: Paris has a population of 2.16 million and is in France.
Tool calls: 1 | Time: 1.8s
Waiting for background skill extraction... done.

...

============================================================
FINAL STATE
============================================================

Managed skills (1):
  - City Info Lookup: Looks up city facts using city_lookup tool...

Quality gate metrics:
  Candidates seen:       2
  Revisions promoted:    2
  Spec-gate rejected:    0
  Safety-gate rejected:  0

Re-run the same example to see warm-start behavior (skills loaded immediately).
```

## Key Observations

- **Round 1**: no skills → agent figures it out from scratch
- **Round 2+**: skill loaded → agent has a "checklist" to follow
- **Re-run**: warm-start, skill available from the first task
- **Gate metrics**: shows how many candidates passed quality checks

## Configuration Variants

### Add Human Approval

```go
evolution.WithHumanGate(evolution.NewCreateOnlyHoldGate())
```

New skills will be held in `pending_approval` state until you approve
them programmatically:

```go
svc := evolution.NewApprovalService(store, pointer, nil)
pending, _ := svc.ListPending(ctx, evolution.ListPendingOpts{})
svc.Decide(ctx, evolution.ApprovalDecision{
    RevisionID: pending[0].RevisionID,
    SkillID:    pending[0].SkillID,
    Approved:   true,
    Reviewer:   "you@example.com",
})
```

### Shadow Mode (observe without enforcing)

```go
evolution.WithApprovalGateShadow(true)
```

Gates evaluate and log but never block promotion — useful for rollout.

## Files Generated

After running, you'll see:

```
managed_skills/
  city-info-lookup/
    SKILL.md              ← the extracted skill

managed_skills_revisions/
  city-info-lookup/
    revisions/
      20260501T.../
        meta.json         ← immutable revision snapshot
    audit.log             ← append-only audit trail
    active.txt            ← points to current revision
```
