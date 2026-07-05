# Pensieve Context Self-Pruning

Demonstrates the [Pensieve paradigm](https://arxiv.org/abs/2602.12108): the LLM actively manages its own visible context instead of relying only on external truncation.

## What this shows

| Tool | Purpose |
|------|---------|
| `check_budget` | Report total, visible, and masked event counts |
| `note` | Save a distilled fact to session state |
| `notes_index` | List note keys and short previews cheaply |
| `read_notes` | Load full note bodies when needed |
| `delete_context` | Mask processed event IDs from LLM-visible history |

Masked events stay in the session for audit but are not sent back to the model on later turns.

## Prerequisites

- Go 1.24+
- `OPENAI_API_KEY` (or compatible endpoint via `OPENAI_BASE_URL`)

## Run

```bash
cd examples/pensieve
export OPENAI_API_KEY="your-api-key"
go run . -model deepseek-v4-flash
```

## Suggested conversation

1. Send several turns of detailed messages (simulate a long thread).
2. Ask: *"Summarize what we discussed, save key facts as notes, then prune older turns from your context."*
3. Use `/budget` to see visible vs masked counts.
4. Ask a follow-up that should rely on notes, not pruned events.

## Architecture

```text
User → Runner → LLMAgent → tool/context (Pensieve tools) → Session (masking + note state)
```

Session masking lives in `session/`; tools live in `tool/context/`.
