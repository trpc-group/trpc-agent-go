# WebFetch Tools

This directory contains web fetching implementations for the trpc-agent-go framework.

## Implementations

###  Client-Side Fetch (Current HTTP Implementation)
Your tool directly fetches content and returns it to the agent.

**How it works:**
- The agent framework makes HTTP requests on behalf of the LLM
- Content is fetched, processed, and returned as tool results
- Provides full control over fetching, parsing, and filtering

**Implementation:** `httpfetch/`

###  LLM Server-Side Fetch (Claude/Gemini Style)
The LLM provider handles fetching; you just configure and enable the tool.

**How it works:**
- The LLM provider's infrastructure fetches web content
- You only provide configuration (domain filters, limits, etc.)
- Content fetching happens on the provider's side, reducing latency

**Implementations:** 
- `geminifetch/`
- `claudefetch/` - Reserved for future use


