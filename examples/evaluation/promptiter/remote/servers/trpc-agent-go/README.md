# tRPC-Agent-Go Candidate Service

This example exposes the multinode sports recap candidate agent through the `server/trpcagent` HTTP protocol so PromptIter can optimize its instruction surfaces remotely.

It is the Go reference server for the remote PromptIter example.

## Run

```bash
cd examples/evaluation/promptiter/remote/servers/trpc-agent-go
export OPENAI_API_KEY="your-openai-compatible-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . -model deepseek-v3.2
```

The service listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/promptiter-sports-recap-agent` by default.

Then run PromptIter from the Go remote example:

```bash
cd ../..
export OPENAI_API_KEY="your-openai-compatible-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
go run . -candidate-target http://127.0.0.1:8081
```
