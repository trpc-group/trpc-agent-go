# Infinity Reranker Example

This example demonstrates how to use the Infinity Reranker implementation, which connects to self-hosted or cloud-based Rerank inference services.

## Terminology

- **Infinity**: Open-source high-performance inference engine (https://github.com/michaelfeil/infinity)
- **TEI (Text Embeddings Inference)**: Official Hugging Face inference engine for embeddings and reranking


## Prerequisites

You need a running Rerank service. This directory includes deployment scripts for Infinity and TEI:

- `deploy_infinity.py`: Deploy using Infinity (supports auto GPU/CPU detection)
- `deploy_reranker.py`: Deploy using Transformers + FastAPI

You can also use:
- **Hugging Face Inference Endpoints**: Managed service at https://ui.endpoints.huggingface.co/

## Usage

1. Deploy a Rerank service (choose one):
   ```bash
   # Using Infinity
   python deploy_infinity.py --model BAAI/bge-reranker-v2-m3 --port 7997
   
   # Using Transformers + FastAPI
   python deploy_reranker.py --model BAAI/bge-reranker-v2-m3 --port 7997
   ```

2. Run the example with endpoint specified:
   ```bash
   # Using local service
   go run main.go -endpoint http://localhost:7997/rerank -model BAAI/bge-reranker-v2-m3
   
   # Using Hugging Face Inference Endpoint
   go run main.go -endpoint https://your-endpoint.hf.space/rerank -model BAAI/bge-reranker-v2-m3
   ```

3. Set OpenAI credentials for embedding comparison:
   ```bash
   export OPENAI_API_KEY=sk-...
   export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
   ```

## Example Output

This example compares Embedding Similarity (Bi-Encoder) with Reranker Scores (Cross-Encoder):

```
Using reranker endpoint: http://localhost:7997/rerank
Using embedding model: text-embedding-3-small


======================================================================
Case: Lexical Overlap Trap
Query: How to kill a Python process?
======================================================================

--- Embedding Similarity (Bi-Encoder) ---
1. [Score: 0.7179645] Use kill -9 PID or pkill python to terminate a Python process.
2. [Score: 0.4849236] Python is a non-venomous snake that kills prey by constriction.
3. [Score: 0.4543524] Kill is a Unix command to send signals to processes.
4. [Score: 0.3367716] The process of learning Python takes about 3 months.
5. [Score: 0.2690866] Python programming language was created by Guido van Rossum.

--- Reranker Scores (Cross-Encoder) ---
1. [Score: 0.9877036] Use kill -9 PID or pkill python to terminate a Python process.
2. [Score: 0.0145583] Python is a non-venomous snake that kills prey by constriction.
3. [Score: 0.0055378] Kill is a Unix command to send signals to processes.
4. [Score: 0.0004349] The process of learning Python takes about 3 months.
5. [Score: 0.0000740] Python programming language was created by Guido van Rossum.
```

**Key Observations**: Rerankers use cross-encoder architecture to better understand semantic relationships between query and documents, providing more accurate relevance scores compared to bi-encoder based embedding similarity.
