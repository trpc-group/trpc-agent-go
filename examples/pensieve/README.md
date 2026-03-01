# Pensieve Context Management Example

This example demonstrates the **Pensieve paradigm** — allowing an LLM to actively manage its own context window using `trpc-agent-go`'s context management tools.

## Scenario

A **research assistant** agent that:

1. Searches the web for information (simulated `web_search` tool with realistic payloads)
2. Distils key findings into persistent **notes** (`note` tool)
3. **Prunes** raw search results from its visible context (`delete_context` tool)
4. **Recalls** distilled knowledge later via `read_notes`
5. Monitors context pressure with `check_budget`

This mirrors a real production pattern where an agent processes many large tool outputs over a long session and must keep its context lean without losing critical data.

> **Reference**: [The Pensieve Paradigm: Stateful Language Models Mastering Their Own Context](https://arxiv.org/abs/2602.12108)

## Tools

| Tool | Icon | Purpose |
|------|------|---------|
| `web_search` | 🔍 | Simulated web search returning large result payloads |
| `check_budget` | 📊 | Report total/visible/masked event counts |
| `note` | 📝 | Save a persistent note (key + content) to session state |
| `read_notes` | 📖 | Retrieve all saved notes |
| `delete_context` | 🗑️ | Mask specific events from visible context |

## Prerequisites

- Go 1.21 or later
- Valid OpenAI-compatible API key

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for the model service | (required) |
| `OPENAI_BASE_URL` | Base URL for the API endpoint | `https://api.openai.com/v1` |

## Usage

```bash
cd examples/pensieve
export OPENAI_API_KEY="your-api-key"
go run .
```

### Custom Model

```bash
go run . -model gpt-4o
```

### Non-Streaming Mode

```bash
go run . -streaming=false
```

## Suggested Conversation

Try these queries in sequence to see context management in action:

```
👤 You: Research transformer architectures and attention mechanisms
   → Agent searches, saves notes, prunes raw results

👤 You: Now research climate change and carbon emissions
   → Agent searches again, saves new notes, prunes again

👤 You: Also look into quantum computing progress
   → Third search, more notes saved, more pruning

👤 You: Summarise all your findings so far
   → Agent calls read_notes to recall everything it distilled
```

After the third query, the agent has processed thousands of tokens of search results, but its visible context remains lean because it pruned the raw data after distilling each batch into notes.

## How It Works

```
User Query
    │
    ▼
┌─────────────┐
│ web_search   │  ← Large search results added to context
└──────┬──────┘
       │
       ▼
┌─────────────┐
│ note         │  ← Key findings saved to session state
└──────┬──────┘
       │
       ▼
┌─────────────────┐
│ delete_context   │  ← Raw search result events masked from view
└──────┬──────────┘
       │
       ▼
  Context stays lean. Notes persist across pruning.
```

## Architecture

```
pensieve-research-agent
├── web_search tool          (simulated, returns large payloads)
├── Pensieve tools           (from tool/context package)
│   ├── check_budget
│   ├── note
│   ├── delete_context
│   └── read_notes
├── LLMAgent                 (llmagent with Pensieve instruction)
└── Runner                   (manages session lifecycle)
```
