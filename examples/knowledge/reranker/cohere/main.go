//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// This example demonstrates how to use Cohere Reranker and compare with embedding similarity.
//
// Integration Guide:
//
//	reranker, err := cohere.New(
//	    cohere.WithTopN(5),
//	)
//	if err != nil {
//	    // handle error
//	}
//	k := knowledge.New(
//	    knowledge.WithReranker(reranker),
//	)
//
// Required environment variables:
//   - COHERE_API_KEY: Your Cohere API key
//   - OPENAI_API_KEY: Your OpenAI API key for embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/cohere"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
)

var (
	apiKey         = flag.String("apikey", os.Getenv("COHERE_API_KEY"), "Cohere API key (default: COHERE_API_KEY env)")
	modelName      = flag.String("model", "rerank-english-v3.0", "Cohere model name")
	embeddingModel = flag.String("embedding-model", "text-embedding-3-small", "Embedding model name")
)

type testCase struct {
	name      string
	query     string
	documents []string
}

func main() {
	flag.Parse()
	ctx := context.Background()

	if *apiKey == "" {
		log.Fatal("Please set COHERE_API_KEY environment variable or use --apikey flag")
	}

	fmt.Printf("Using Cohere model: %s\n", *modelName)
	fmt.Printf("Using embedding model: %s\n", *embeddingModel)
	fmt.Println()

	testCases := []testCase{
		{
			name:  "Lexical Overlap Trap",
			query: "How to kill a Python process?",
			documents: []string{
				"Use kill -9 PID or pkill python to terminate a Python process.",
				"Python is a non-venomous snake that kills prey by constriction.",
				"Python programming language was created by Guido van Rossum.",
				"The process of learning Python takes about 3 months.",
				"Kill is a Unix command to send signals to processes.",
			},
		},
		{
			name:  "Semantic Precision",
			query: "What year was Bitcoin created?",
			documents: []string{
				"Bitcoin was created in 2009 by Satoshi Nakamoto.",
				"Bitcoin is a decentralized digital currency.",
				"The Bitcoin whitepaper was published in 2008.",
				"Bitcoin mining requires significant computational power.",
				"Cryptocurrency has grown significantly since 2010.",
			},
		},
		{
			name:  "Implicit Answer",
			query: "Can I use React without Node.js?",
			documents: []string{
				"React can be included via CDN script tags without any build tools.",
				"Node.js is commonly used for React development.",
				"React is a JavaScript library for building user interfaces.",
				"npm is the package manager for Node.js.",
				"Create React App requires Node.js to be installed.",
			},
		},
	}

	for _, tc := range testCases {
		fmt.Printf("\n%s\n", strings.Repeat("=", 70))
		fmt.Printf("Case: %s\n", tc.name)
		fmt.Printf("Query: %s\n", tc.query)
		fmt.Printf("%s\n", strings.Repeat("=", 70))

		runComparison(ctx, tc.query, tc.documents)
	}
}

func runComparison(ctx context.Context, queryText string, documents []string) {
	emb := util.NewOpenAIEmbedder(*embeddingModel)

	// 1. Calculate embedding similarity scores
	fmt.Println("\n--- Embedding Similarity (Bi-Encoder) ---")
	embeddingScores := util.CalculateEmbeddingScores(ctx, queryText, documents, emb)
	util.PrintEmbeddingResults(embeddingScores, documents)

	// 2. Rerank with Cohere
	fmt.Println("\n--- Reranker Scores (Cohere Cross-Encoder) ---")
	candidates := make([]*reranker.Result, len(documents))
	for i, doc := range documents {
		candidates[i] = &reranker.Result{
			Document: &document.Document{Content: doc},
			Score:    embeddingScores[i],
		}
	}

	query := &reranker.Query{
		Text:       queryText,
		FinalQuery: queryText,
	}

	r, err := cohere.New(
		cohere.WithAPIKey(*apiKey),
		cohere.WithModel(*modelName),
	)
	if err != nil {
		log.Printf("Create cohere reranker failed: %v", err)
		return
	}

	results, err := r.Rerank(ctx, query, candidates)
	if err != nil {
		log.Printf("Rerank failed: %v", err)
		return
	}
	printRerankerResults(results)
}

func printRerankerResults(results []*reranker.Result) {
	for i, res := range results {
		fmt.Printf("%d. [Score: %.7f] %s\n", i+1, res.Score, res.Document.Content)
	}
}
