# Code Review Agent (Go)

Automated AI code reviewer using trpc-agent-go + Hy3 LLM + SQLite storage.

## Quick Start

```bash
go mod tidy
export TRPC_AGENT_API_KEY=your-key
export TRPC_AGENT_BASE_URL=https://tokenhub.tencentmaas.com/v1
go run main.go -file /path/to/code.go
```

## Architecture

```
User → Code Review Agent (Hy3 LLM)
         ├── review_code() → Structure input
         ├── save_review()  → SQLite persistence
         └── LLM analysis   → Detailed review report
```
