# sks/trpc-agent-go fork guide

This repository is a long-lived fork of [`trpc-group/trpc-agent-go`](https://github.com/trpc-group/trpc-agent-go). Genie and StackGen pin this fork via `replace` directives. Treat every sync as a product decision, not a blind rebase.

## Remotes

```bash
git remote add upstream ssh://git@github.com/trpc-group/trpc-agent-go.git   # once
git fetch upstream main
git fetch origin main
```

## What this fork must keep (core deltas)

These are Genie-facing behaviors. Prefer **ours** when a sync conflict touches the same lines:

| Area | Why Genie needs it | Hot paths |
|------|--------------------|-----------|
| Pensieve / `tool/context` | `note`, `delete_context`, `check_budget`, note receipt + budget | `tool/context/*` (sks-only package) |
| Session masking | Soft-hide events; notes survive pruning | `session/session.go`, `session/summary/*` |
| Langfuse hooks | `AttributeRewriter`, identity options, `CachedTokens` | `telemetry/langfuse/*` |
| Anthropic stream retry | Transient stream failures + truncated `tool_use` repair | `model/anthropic/*` |
| Function-tool JSON schema | Preserve validation constraints; always-on leading-JSON repair | `tool/function/function_tool.go` |
| GORM storage/memory | Optional backends used by some hosts | `storage/gorm/*`, `memory/gorm/*` |

Everything else: prefer **upstream** unless a Genie pin or test proves otherwise.

## OpenAI Responses (AIOS-935)

Stock OpenAI reasoning + function tools should use the nested module
`model/openai/responses` (upstream [PR #2483](https://github.com/trpc-group/trpc-agent-go/pull/2483)),
not Chat Completions `model/openai`. Chat Completions stays the default for
Azure and custom OpenAI-compatible hosts.

```go
import openairesponses "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"

m := openairesponses.New(modelName,
    openairesponses.WithAPIKey(apiKey),
    openairesponses.WithBaseURL(baseURL), // SDK appends "responses"
    openairesponses.WithStore(false),     // stateless; no plaintext CoT store
)
```

With `store=false`, the adapter requests `include=["reasoning.encrypted_content"]`,
stores the blob on `Message.ReasoningSignature`, and replays it on later turns
**without** inventing `rs_replay_*` ids. Plaintext `ReasoningContent` alone is
never sent as a reasoning input item (that is what caused dogfood 404s).
Function-call / tool-output items still convert so multi-step tool loops work.

Try locally:

```bash
export OPENAI_API_KEY=sk-...
cd examples/openairesponses
go run ./chat -streaming=true
go run ./tools -streaming=true
```

Nested module tests: `cd model/openai/responses && go test ./...`

Genie should construct this type for qualified stock OpenAI models and keep
mapping `reasoning_profile` → native `ReasoningEffort` on `GenerationConfig`.
Do not silently fall back from Responses to Chat Completions after a request
failure.

## Sync playbook (every ~2 weeks, or before a Genie bump)

1. **Branch from current fork main**
   ```bash
   git switch main && git pull --ff-only origin main
   git switch -c sync/upstream-main-$(date +%Y-%m-%d)
   ```
2. **Merge, do not rebase history**
   ```bash
   git merge --no-ff upstream/main
   ```
   Merge keeps the fork's commit graph and makes “what did we invent?” searchable with `git log origin/main --not upstream/main`.
3. **Resolve by concern, not by file**
   - Same concern on both sides → combine.
   - Core delta vs unrelated polish → keep core delta; take upstream polish elsewhere.
   - Never `--ours` / `--theirs` a whole package that mixes Genie behavior with upstream refactors.
4. **Validate the Genie surface**
   ```bash
   go test ./tool/context ./tool/function ./session ./session/summary ./telemetry/langfuse ./model/anthropic -count=1
   go build ./...
   ```
5. **Open a PR into `sks/main`**, then bump Genie's `replace` pin only after merge.

## How to avoid future conflicts

1. **Contribute upstream first when possible**  
   Pensieve-adjacent and stream-retry work should land in `trpc-group` when maintainers will take it. Fork-only code is the conflict tax.

2. **Keep fork deltas narrow and named**  
   Prefer additive APIs (`WithStreamRetry`, `AttributeRewriter`, `NoteSaveReceipt`) over rewriting upstream control flow. Additive surfaces merge cleanly; forked control flow does not.

3. **Isolate sks-only packages**  
   `tool/context` lives as its own package. Do not fold Pensieve helpers into `session` or `tool/function` unless upstream already owns that seam.

4. **One concern per PR on the fork**  
   Do not mix “sync upstream” with “new Genie feature”. Sync PRs should only resolve merge conflicts and add this guide / pin notes.

5. **Pin Genie to a merged commit SHA**  
   Genie's `replace trpc.group/trpc-go/trpc-agent-go => github.com/sks/trpc-agent-go v0.0.0-…` must point at a commit that is on `sks/main` after the sync PR merges. Never pin a long-lived feature branch.

6. **Staged sync when overlap is large**  
   If `comm -12` of changed paths exceeds ~40 files, split like PR #25: first apply non-overlapping upstream commits, then reconcile the connected conflict set. Do not open a 1,300-file compare in the GitHub UI ([compare UI fails at that size](https://github.com/sks/trpc-agent-go/compare/main...trpc-group%3Atrpc-agent-go%3Amain)).

## Commands to inspect divergence

```bash
# commits only on the fork / only on upstream
git rev-list --left-right --count origin/main...upstream/main

# overlapping paths (likely conflict set)
comm -12 \
  <(git diff --name-only $(git merge-base origin/main upstream/main)..origin/main | sort -u) \
  <(git diff --name-only $(git merge-base origin/main upstream/main)..upstream/main | sort -u)
```

## Do not

- Force-push `main`.
- Drop `tool/context` or masked-event summary behavior during a sync.
- Squash-merge sync PRs if you still need to cherry-pick individual upstream commits later (merge commits are fine).
