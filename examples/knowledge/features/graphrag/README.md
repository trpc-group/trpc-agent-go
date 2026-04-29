# GraphRAG Code Chat

Ask a codebase question, let the agent find the right symbols, then traverse the call graph instead of guessing from keyword hits.

This example indexes the `trpc-go` repository into:

- Apache AGE for code graph nodes and edges.
- pgvector for semantic seed search.
- A chat agent with `code_graph_search`, `code_graph_traverse`, and `code_graph_find_paths`.

It is useful for questions where plain vector search usually stops too early:

- "Where is this mechanism configured?"
- "How does this request flow from client to server?"
- "Which functions participate in this error handling path?"
- "What is connected to this symbol, and why?"

## What It Shows

The case demonstrates a GraphRAG workflow over real Go code:

1. Search the codebase semantically to find likely seed symbols.
2. Resolve those results to stable graph node IDs.
3. Traverse callers, callees, or dependency edges around those symbols.
4. Answer with concrete files, functions, and graph evidence.

Compared with a normal RAG example, this one can move from a fuzzy question like "timeout propagation" to specific code such as:

- `client.client.Invoke`
- `codec.msg.WithRequestTimeout`
- `server.service.handle`
- `server.mayConvert2FullLinkTimeout`
- `server.mayConvert2NormalTimeout`

## Demo Case: Timeout Propagation in tRPC-Go

Start the chat with existing indexed data:

```bash
cd examples/knowledge/features/graphrag
go run . -recreate=false
```

Example startup:

```text
GraphRAG Chat Demo
Model: claude-4-5-sonnet-20250929
Streaming: true
==================================================
Embedding: server:277357 (1024 dimensions)
Skipped graph source loading; using existing AGE graph and pgvector data

Chat ready. Session: graph-demo-session-1777467345
Type '/exit' to end the conversation.
Try: Find code related to client RPC invocation in trpc-go, traverse its callees, and explain the nearby call graph.
==================================================
```

Ask:

```text
Help me check the timeout propagation chain in trpc-go.
```

The agent first searches for timeout-related seed code:

```text
Tool calls (2):
  [1] code_graph_search
      query: timeout propagation context deadline
      filter:
        metadata.trpc_ast_repo_name = trpc-go
        metadata.trpc_ast_scope = code

  [2] code_graph_search
      query: WithTimeout SetTimeout timeout handling
      filter:
        metadata.trpc_ast_repo_name = trpc-go
        metadata.trpc_ast_scope = code
        metadata.trpc_ast_type in [Function, Method]
```

Typical results include:

```text
documents: 3
  [1] server.WithTimeout
      file: trpc-go server/options.go:226-231
      sig: func WithTimeout(t time.Duration) Option

  [2] client.WithTimeout
      file: trpc-go client/options.go:317-322
      sig: func WithTimeout(t time.Duration) Option
```

Then it narrows to server and client timeout handling:

```text
Tool calls:
  code_graph_search query="timeout request handling context"
  code_graph_search query="client timeout context deadline"
```

The relevant seed symbols become:

```text
server.Options
server.WithDisableRequestTimeout
client.BackendConfig
client.client.Invoke
server.mayConvert2NormalTimeout
server.mayConvert2FullLinkTimeout
```

After that, the agent uses graph traversal to inspect the actual call neighborhood:

```text
Tool calls:
  code_graph_traverse
      direction: out
      edge_types: [CALLS]
      max_depth: 2
      start_ids: [node:...]
```

Example graph result:

```text
nodes: 21
  [1] Invoke
      full: trpc.group/trpc-go/trpc-go/client.client.Invoke
      file: trpc-go client/client.go:55-99

  [2] fixFilters
      full: trpc.group/trpc-go/trpc-go/client.client.fixFilters
      file: trpc-go client/client.go:206-218

edges: 21
  [1] CALLS: client.client.Invoke -> client.client.getOptions
  [2] CALLS: client.client.Invoke -> client.client.updateMsg
  ... more edges omitted
```

The final answer can explain the propagation chain with concrete code anchors:

```text
client.client.Invoke
  -> applies client timeout with context.WithTimeout
  -> writes remaining deadline into msg.WithRequestTimeout
  -> protocol encoding carries RequestTimeout

codec.msg.WithRequestTimeout / RequestTimeout
  -> stores timeout on the framework message

server.service.handle
  -> reads msg.RequestTimeout
  -> compares upstream timeout with server Options.Timeout
  -> applies the smaller timeout using context.WithTimeout

server.mayConvert2FullLinkTimeout / mayConvert2NormalTimeout
  -> maps timeout errors to full-link or normal server timeout codes
```

Good follow-up questions:

```text
如何在trpc-go中配置超时时间？
trpc-go超时时间的默认值是多少？
除了超时，trpc-go还有哪些常见的错误和异常情况？
```

These work well because the first question asks for configuration symbols, the second forces the agent to inspect defaults, and the third expands from timeout errors to the surrounding error taxonomy.

## Run From Scratch

Set model and embedding options:

```bash
export OPENAI_API_KEY=sk-xxxx
export OPENAI_BASE_URL=https://api.openai.com/v1
export MODEL_NAME=claude-4-5-sonnet-20250929
export EMBEDDING_MODEL=server:277357
export EMBEDDING_DIMENSION=1024
```

Set AGE and pgvector. `AGE_DSN` and `PGVECTOR_DSN` can override the individual fields.

```bash
export AGE_HOST=127.0.0.1
export AGE_PORT=5432
export AGE_USER=root
export AGE_PASSWORD=123
export AGE_DATABASE=contextengine
export AGE_GRAPH_NAME=knowledge_graph

export PGVECTOR_HOST=127.0.0.1
export PGVECTOR_PORT=5432
export PGVECTOR_USER=root
export PGVECTOR_PASSWORD=123
export PGVECTOR_DATABASE=contextengine
export PGVECTOR_TABLE=trpc_agent_go_graph
```

Load or reload the repository graph and vector seed index:

```bash
go run . -recreate=true
```

Reuse existing AGE and pgvector data:

```bash
go run . -recreate=false
```

## Useful Flags

```bash
go run . \
  -recreate=false \
  -streaming=true \
  -model=claude-4-5-sonnet-20250929 \
  -embedding-model=server:277357 \
  -embedding-dimension=1024
```

Key flags:

- `-recreate=true`: drop and rebuild the AGE graph and pgvector table.
- `-recreate=false`: skip loading and use existing indexed data.
- `-query="..."`: run one initial question before entering interactive chat.
- `-progress-step=100`: control graph loading progress logs.

## Graph Viewer

The viewer is in [viewer](./viewer). It reads the same AGE graph and shows a lightweight UI for inspecting nodes, edges, content, and metadata.

```bash
go run ./viewer -addr=0.0.0.0:3012
```

Open:

```text
http://127.0.0.1:3012
```

For remote access, bind to `0.0.0.0` and use the machine IP.
