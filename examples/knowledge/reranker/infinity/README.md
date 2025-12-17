# Infinity Reranker Example

This example demonstrates how to use the Infinity Reranker implementation, which connects to a self-hosted Infinity or TEI instance.

## Prerequisites

You need a running Infinity instance. Choose one of the following methods:

### Option 1: Using Python Script (Recommended)

```bash
# Install dependencies
pip install transformers torch fastapi uvicorn

# Start server (auto-detect GPU/CPU)
python deploy_infinity.py

# Or with custom options
python deploy_infinity.py --model BAAI/bge-reranker-v2-m3 --port 7997 --device cpu
```

### Option 2: Using Docker

```bash
docker run -p 7997:7997 michaelfeil/infinity:latest \
  --model-id BAAI/bge-reranker-v2-m3 \
  --port 7997
```

### Option 3: Using infinity-emb

```bash
pip install infinity-emb[all]
infinity_emb v2 --model-id BAAI/bge-reranker-v2-m3 --port 7997
```

## Usage

1.  Set environment variables:
    ```bash
    # Optional, defaults to localhost:7997
    export INFINITY_URL=http://localhost:7997/rerank

    # Required for embedding comparison (OpenAI embedder)
    export OPENAI_API_KEY=sk-...
    export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional, custom endpoint
    ```

2.  Run the example:
    ```bash
    # Default settings
    go run main.go

    # Or with custom endpoint and model
    go run main.go -endpoint http://localhost:7997/rerank -model bge-reranker-v2-m3
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

======================================================================
Case: Semantic Precision
Query: What year was Bitcoin created?
======================================================================

--- Embedding Similarity (Bi-Encoder) ---
1. [Score: 0.7111065] Bitcoin was created in 2009 by Satoshi Nakamoto.
2. [Score: 0.5625346] The Bitcoin whitepaper was published in 2008.
3. [Score: 0.4725427] Bitcoin is a decentralized digital currency.
4. [Score: 0.3695762] Cryptocurrency has grown significantly since 2010.
5. [Score: 0.2746695] Bitcoin mining requires significant computational power.

--- Reranker Scores (Cross-Encoder) ---
1. [Score: 0.9946451] Bitcoin was created in 2009 by Satoshi Nakamoto.
2. [Score: 0.0484865] Cryptocurrency has grown significantly since 2010.
3. [Score: 0.0332981] The Bitcoin whitepaper was published in 2008.
4. [Score: 0.0029873] Bitcoin is a decentralized digital currency.
5. [Score: 0.0000177] Bitcoin mining requires significant computational power.

======================================================================
Case: Implicit Answer
Query: Can I use React without Node.js?
======================================================================

--- Embedding Similarity (Bi-Encoder) ---
1. [Score: 0.6563176] Node.js is commonly used for React development.
2. [Score: 0.6455354] Create React App requires Node.js to be installed.
3. [Score: 0.5608944] React can be included via CDN script tags without any build tools.
4. [Score: 0.5521758] React is a JavaScript library for building user interfaces.
5. [Score: 0.3723294] npm is the package manager for Node.js.

--- Reranker Scores (Cross-Encoder) ---
1. [Score: 0.9883498] Node.js is commonly used for React development.
2. [Score: 0.9765134] Create React App requires Node.js to be installed.
3. [Score: 0.1655318] React is a JavaScript library for building user interfaces.
4. [Score: 0.0802073] React can be included via CDN script tags without any build tools.
5. [Score: 0.0011102] npm is the package manager for Node.js.
```

## Available Reranker Models

| Model | Description |
|-------|-------------|
| BAAI/bge-reranker-v2-m3 | Multilingual, recommended |
| BAAI/bge-reranker-large | English, high quality |
| BAAI/bge-reranker-base | English, balanced |
| jinaai/jina-reranker-v2-base-multilingual | Multilingual alternative |
