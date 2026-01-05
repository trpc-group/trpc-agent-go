# Cohere Reranker Example

This example demonstrates how to use the Cohere Reranker implementation.

## Usage

1.  Set your API keys:
    ```bash
    # Required for Cohere reranker
    export COHERE_API_KEY=your_key_here

    # Required for embedding comparison (OpenAI embedder)
    export OPENAI_API_KEY=sk-...
    export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional, custom endpoint
    ```

2.  Run the example:
    ```bash
    # Default uses rerank-english-v3.0
    go run main.go

    # Or specify API key and model via flags
    go run main.go -apikey your_key_here -model rerank-multilingual-v3.0
    ```

## Example Output

This example compares Embedding Similarity (Bi-Encoder) with Reranker Scores (Cross-Encoder):

```
Using Cohere model: rerank-english-v3.0
Using embedding model: text-embedding-3-small


======================================================================
Case: Lexical Overlap Trap
Query: How to kill a Python process?
======================================================================

--- Embedding Similarity (Bi-Encoder) ---
1. [Score: 0.7179645] Use kill -9 PID or pkill python to terminate a Python process.
2. [Score: 0.4849236] Python is a non-venomous snake that kills prey by constriction.
3. [Score: 0.4543586] Kill is a Unix command to send signals to processes.
4. [Score: 0.3367716] The process of learning Python takes about 3 months.
5. [Score: 0.2690866] Python programming language was created by Guido van Rossum.

--- Reranker Scores (Cohere Cross-Encoder) ---
1. [Score: 0.9996594] Use kill -9 PID or pkill python to terminate a Python process.
2. [Score: 0.1460872] Kill is a Unix command to send signals to processes.
3. [Score: 0.0263053] Python is a non-venomous snake that kills prey by constriction.
4. [Score: 0.0000954] Python programming language was created by Guido van Rossum.
5. [Score: 0.0000057] The process of learning Python takes about 3 months.

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

--- Reranker Scores (Cohere Cross-Encoder) ---
1. [Score: 0.9985675] Bitcoin was created in 2009 by Satoshi Nakamoto.
2. [Score: 0.9780936] The Bitcoin whitepaper was published in 2008.
3. [Score: 0.4859275] Cryptocurrency has grown significantly since 2010.
4. [Score: 0.0153653] Bitcoin is a decentralized digital currency.
5. [Score: 0.0016875] Bitcoin mining requires significant computational power.

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

--- Reranker Scores (Cohere Cross-Encoder) ---
1. [Score: 0.9973888] Node.js is commonly used for React development.
2. [Score: 0.9906600] Create React App requires Node.js to be installed.
3. [Score: 0.8807970] React can be included via CDN script tags without any build tools.
4. [Score: 0.1553346] React is a JavaScript library for building user interfaces.
5. [Score: 0.0029694] npm is the package manager for Node.js.
```
