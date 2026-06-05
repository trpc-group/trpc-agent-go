//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the LLM-based query enhancer for multi-turn
// knowledge-enhanced chat.
//
// Without a query enhancer, follow-up questions like "how does it work?" lose
// context because the pronoun "it" cannot be resolved by the retriever.
// The LLMEnhancer rewrites the query using conversation history so that the
// retriever receives a standalone, search-optimized query.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint, defaults to https://api.openai.com/v1
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-v4-flash
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-v4-flash
//	go run main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	defaultModelName = "deepseek-v4-flash"
	vectorStore      = flag.String("vectorstore", "inmemory",
		"Vector store type: inmemory|sqlitevec|pgvector|tcvector|elasticsearch")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	modelName := util.GetEnvOrDefault("MODEL_NAME", defaultModelName)

	fmt.Println("🔄 Query Enhancer Demo — Multi-turn Knowledge Chat")
	fmt.Println("===================================================")
	fmt.Printf("Model: %s\n", modelName)

	// 1. Create sources
	sources := []source.Source{
		file.New(
			[]string{util.ExampleDataPath("file/llm.md")},
			file.WithName("LLM Docs"),
		),
		file.New(
			[]string{util.ExampleDataPath("file/golang.md")},
			file.WithName("Golang Docs"),
		),
	}

	// 2. Create vector store
	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)

	// 3. Create the LLM model (shared between agent and query enhancer)
	llm := openaimodel.New(modelName)

	// 4. Create the LLM query enhancer, wrapped with a debug decorator
	// that prints the original and rewritten query for each enhancement.
	//
	// The decorator pattern works because query.Enhancer is a single-method
	// interface — any struct implementing EnhanceQuery can wrap another.
	enhancer := &debugEnhancer{inner: query.NewLLMEnhancer(llm)}

	// 5. Create knowledge base with query enhancer
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources(sources),
		knowledge.WithQueryEnhancer(enhancer),
	)

	if err := kb.Load(ctx); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}
	util.WaitForIndexRefresh(storeType)

	// 6. Create search tool and agent
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb, knowledgetool.WithMaxResults(3))
	agent := llmagent.New(
		"knowledge-assistant",
		llmagent.WithModel(llm),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// 7. Create runner with session (multi-turn needs persistent session)
	r := runner.NewRunner(
		"enhancer-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// 8. Simulate a multi-turn conversation.
	// The second and third queries contain pronouns / references that only
	// make sense in the context of the first query. Without the query
	// enhancer, the retriever would search for "it" or "the above" verbatim.
	conversation := []string{
		"What are Large Language Models?",
		"How does it handle context length?",
		"Compare the above with traditional search engines",
	}

	userID := "demo-user"
	sessionID := "multi-turn-session"

	for i, q := range conversation {
		fmt.Printf("\n── Turn %d ─────────────────────────────────\n", i+1)
		fmt.Printf("👤 User: %s\n", q)

		eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage(q))
		if err != nil {
			log.Printf("Query failed: %v", err)
			continue
		}

		var fullResponse strings.Builder
		for evt := range eventChan {
			util.PrintEventWithToolCalls(evt)

			if len(evt.Response.Choices) == 0 {
				continue
			}
			choice := evt.Response.Choices[0]
			if choice.Delta.Content != "" {
				fullResponse.WriteString(choice.Delta.Content)
			}
			if evt.IsFinalResponse() {
				answer := fullResponse.String()
				if answer == "" {
					answer = choice.Message.Content
				}
				fmt.Printf("\n🤖 Assistant: %s\n", answer)
			}
		}
	}

	fmt.Println("\n✅ Done!")
}

// debugEnhancer wraps a query.Enhancer and prints the before/after query.
// This demonstrates how to observe or extend enhancement behavior using
// Go's interface composition — no framework hooks needed.
type debugEnhancer struct {
	inner query.Enhancer
}

func (d *debugEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
	if len(req.History) > 0 {
		fmt.Printf("   📜 History (%d messages):\n", len(req.History))
		for _, h := range req.History {
			fmt.Printf("      [%s] %s\n", h.Role, truncate(h.Content, 80))
		}
	}

	result, err := d.inner.EnhanceQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	if result.Enhanced != req.Query {
		fmt.Printf("   🔄 Query enhanced: %q -> %q\n", req.Query, result.Enhanced)
	} else {
		fmt.Printf("   🔄 Query unchanged: %q\n", req.Query)
	}
	return result, nil
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}
