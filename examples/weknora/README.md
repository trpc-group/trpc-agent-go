# WeKnora Agent Examples

This directory contains examples demonstrating how to use the WeKnora agent in `trpc-agent-go`.

## Prerequisites

Before running the examples, you need to set up the following environment variables:

```bash
export WEKNORA_BASE_URL="your-weknora-base-url"
export WEKNORA_TOKEN="your-weknora-token"
export WEKNORA_AGENT_ID="your-weknora-agent-id"
export WEKNORA_SESSION_ID="your-weknora-session-id" # if not set, it will auto create one
```

## Examples

### Streaming Chat (`streaming_chat/`)

Demonstrates how to handle streaming responses from a WeKnora agent, which is useful for providing real-time feedback to users.

**To run:**
```bash
cd streaming_chat
go run main.go
```
