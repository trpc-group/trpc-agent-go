# Time Processor Prompt Cache Example

This example compares prompt-cache behavior for `llmagent.WithAddCurrentTime`
and the time request processor.

It is intentionally separate from `examples/promptcache/openai` so the original
OpenAI prompt-cache demo remains unchanged.

## Cases

- `baseline`: no time processor, stable system prompt baseline.
- `date-only`: `WithAddCurrentTime(true)` with the new default date-only system
  prompt.
- `full-datetime`: `WithAddCurrentTime(true)` plus an explicit full timestamp
  format, which changes every turn and is expected to be less cache-friendly.
- `precise-tool`: date-only system context plus the built-in
  `environment_context_current_time` tool for exact clock time.

## Run

```bash
cd examples/promptcache/timeprocessor
export OPENAI_API_KEY="your-api-key"
go run .
```

For OpenAI-compatible services such as Hunyuan:

```bash
export OPENAI_BASE_URL="https://your-compatible-endpoint"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="your-model"
export PROMPT_CACHE_KEY_PREFIX="timeprocessor-demo"
go run . -case all
```

Run a single case:

```bash
go run . -case date-only
go run . -case full-datetime
go run . -case precise-tool
```

`-turn-delay` defaults to `1200ms` so the `full-datetime` case has a different
timestamp on each turn.

## Sample Results

The following sample was collected with Hunyuan `hy3-preview` through the
OpenAI-compatible API on 2026-06-10. Each case has four turns. Prompt cache is
provider-side and best-effort, so exact numbers may vary between runs.

This table is an illustrative demo run, not an isolated benchmark. In a
sequential `-case all` run, `baseline` may warm provider-side cache for later
cases because they share most of the same long stable prompt prefix. Therefore,
do not interpret `date-only` being higher than `baseline` as a design advantage;
`baseline` is only a sanity reference that stable prompts can be cached. The key
comparison for this demo is `date-only` versus `full-datetime`.

| Case | Time Handling | Exact Time Tool | Prompt Tokens | Cached Tokens | Requests With Cache | Tool Calls | Overall Cache Rate |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| `baseline` | No time processor. Stable system prompt only. | Not available | 15,878 | 11,200 | 3/4 | 0 | 70.5% |
| `date-only` | `WithAddCurrentTime(true)` with default date-only system context. | Available but not called in this case | 17,125 | 15,872 | 4/4 | 0 | 92.7% |
| `full-datetime` | `WithAddCurrentTime(true)` with `WithTimeFormat("2006-01-02 15:04:05 MST")`; timestamp changes every turn. | Available but not called in this case | 17,112 | 10,752 | 3/4 | 0 | 62.8% |
| `precise-tool` | Date-only system context; exact time fetched only on demand. | `environment_context_current_time` called on time questions | 17,693 | 13,120 | 3/4 | 2 | 74.2% |

The important comparison is `date-only` versus `full-datetime`, not
`date-only` versus `baseline`: keeping the system prompt stable at date
granularity preserves more of the prompt-cache prefix, while full datetime
invalidates more of the prefix as the timestamp changes. `precise-tool` keeps
the stable date context and still supports exact clock time via
`environment_context_current_time`.

For the sample above, `date-only` improves the cache rate by 29.9 percentage
points over `full-datetime` and reuses 5,120 more cached tokens. The
`precise-tool` case called the built-in time tool on the exact-time turns, so
precise clock values stayed out of the stable system prompt.

## What to Look For

The `date-only` case should keep a stable prompt prefix within the same day,
while `full-datetime` changes the system prompt on every turn. The
`precise-tool` case demonstrates that exact time can be fetched on demand via
`environment_context_current_time` without embedding a volatile timestamp into
every request.

