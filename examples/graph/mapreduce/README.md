# Map-Reduce (Document Chunking → Parallel Summaries → Final Join)

This example demonstrates a classic Map-Reduce (映射‑归约) workflow built with the `graph` package:

- Split a document into overlapping chunks (map inputs)
- Retrieve top‑K relevant chunks by a user question
- Summarize selected chunks in parallel (map phase)
- Join partial summaries into a concise final answer (reduce phase)

It mirrors the “from document chunking to final synthesis” pattern seen in many systems, using the primitives already present in `trpc-agent-go` (fan‑out, diamond fan‑in barrier, custom reducers).

## Run

```bash
cd examples/graph/mapreduce
# Use the provided sample.txt, or pass your own via -file
go run . -model deepseek-chat -file ./sample.txt -top-k 4 -chunk-size 800 -overlap 100
```

Then when prompted, type your question (the “query” for retrieval), for example:

```
What are the main concurrency patterns and caveats in Go?
```

## What It Shows

- Chunking: fixed‑size with overlap to preserve context boundaries.
- Lexical retrieval: simple term‑match scoring to pick top‑K chunks for the question (no external vector DB required).
- Fan‑out mapping: a node returns `[]*graph.Command` targeting the same LLM node with different per‑task inputs.
- Barrier fan‑in: collect each chunk summary and only proceed to “reduce” once all K are ready.
- Reduce join: an LLM synthesizes a final answer from the partial summaries.

## Files

- `main.go` — the end‑to‑end example entrypoint.
- `sample.txt` — a small demo document you can try immediately.

## Notes

- The model name defaults to `deepseek-chat` for consistency with other examples. Provide your own model via `-model`.
- The example uses only built‑in retrieval (string matching). Replace retrieval with embeddings or your own store if desired.
