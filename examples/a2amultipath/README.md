# A2A Multi-Path Example (One Port, Multiple Agents)

This example shows the idiomatic way to expose **multiple A2A agents from a
single HTTP port** by giving each agent a different **base path** (base URL).

Instead of sending an `agent_name` parameter to route requests, the client
selects the target agent by choosing the agent's URL:

- Math agent: `http://localhost:8888/agents/math`
- Weather agent: `http://localhost:8888/agents/weather`

## How It Works

1. Create one A2A server per agent (for example, using `a2a.New`).
2. Set a different `a2a.WithHost(...)` URL per agent (the URL includes a path).
3. Mount all A2A servers onto one shared `http.Server` via `server.Handler()`.

## Run

In one terminal:

```bash
cd examples/a2amultipath
go run ./server
```

In another terminal, call different agents by changing `-url`:

```bash
cd examples/a2amultipath
go run ./client -url http://localhost:8888/agents/math -msg "2+2"
go run ./client -url http://localhost:8888/agents/weather -msg "How is it?"
```

You can also fetch each agent's AgentCard:

```bash
curl http://localhost:8888/agents/math/.well-known/agent-card.json
curl http://localhost:8888/agents/weather/.well-known/agent-card.json
```
