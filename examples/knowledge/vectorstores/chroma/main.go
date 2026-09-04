//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using Chroma for vector storage.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-v4-flash
//   - CHROMA_URL: (Optional) Chroma HTTP address, defaults to http://localhost:8000
//   - CHROMA_COLLECTION: (Optional) Chroma collection name, defaults to trpc_example
//   - CHROMA_API_KEY: (Optional) Chroma Cloud API key, enables WithAPIKey
//   - CHROMA_TENANT: (Optional) Chroma tenant, defaults to the server tenant
//   - CHROMA_DATABASE: (Optional) Chroma database, defaults to the server database
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export CHROMA_URL=http://localhost:8000
//	export CHROMA_COLLECTION=trpc_example
//	go run main.go
//
// For Chroma Cloud, export CHROMA_API_KEY instead of CHROMA_URL; when the
// tenant and database are omitted, WithAPIKey resolves them from the Cloud
// identity endpoint automatically.
package main

import (
	"context"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/chroma"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName  = util.GetEnvOrDefault("MODEL_NAME", "deepseek-v4-flash")
	baseURL    = util.GetEnvOrDefault("CHROMA_URL", "http://localhost:8000")
	collection = util.GetEnvOrDefault("CHROMA_COLLECTION", "trpc_example")
	apiKey     = util.GetEnvOrDefault("CHROMA_API_KEY", "")
	tenant     = util.GetEnvOrDefault("CHROMA_TENANT", "")
	database   = util.GetEnvOrDefault("CHROMA_DATABASE", "")
)

func main() {
	ctx := context.Background()

	fmt.Println("🔶 Chroma Vector Store Demo")
	fmt.Println("===========================")

	if apiKey != "" {
		fmt.Printf("📊 Connecting to Chroma Cloud (tenant: %q, database: %q)\n", tenant, database)
	} else {
		fmt.Printf("📊 Connecting to Chroma: %s collection: %s\n", baseURL, collection)
	}

	// Create Chroma store. The default index dimension (1536) matches the
	// OpenAI text-embedding-3-small embedder used below. WithAPIKey sends the
	// key as X-Chroma-Token for Chroma Cloud; with an empty tenant or database
	// it resolves both from the Cloud identity endpoint. Empty auth values are
	// ignored, so the same options work for a local server without auth.
	vs, err := chroma.New(ctx,
		chroma.WithBaseURL(baseURL),
		chroma.WithCollection(collection),
		chroma.WithAPIKey(apiKey),
		chroma.WithTenant(tenant),
		chroma.WithDatabase(database),
	)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	defer vs.Close()

	// The embedder is shared by the knowledge base and the direct store calls.
	emb := openai.New()

	// Create file source
	src := file.New(
		[]string{util.ExampleDataPath("file/llm.md")},
		file.WithName("LLM Docs"),
	)

	// Create knowledge base
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources([]source.Source{src}),
	)

	fmt.Println("\n📥 Loading knowledge into Chroma...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Direct VectorStore API: add a document with its embedding, then run a
	// vector search and a metadata filter search without the knowledge base.
	demoDoc := &document.Document{
		ID:      "chroma-demo-doc",
		Name:    "chroma-intro",
		Content: "Chroma is an open-source vector database for AI applications.",
		Metadata: map[string]any{
			"category": "chroma-demo",
		},
	}
	demoVec, err := emb.GetEmbedding(ctx, demoDoc.Content)
	if err != nil {
		log.Fatalf("Failed to embed demo document: %v", err)
	}
	if err := vs.Add(ctx, demoDoc, demoVec); err != nil {
		log.Fatalf("Failed to add demo document: %v", err)
	}
	fmt.Printf("\n🧪 Direct VectorStore API demo\n  ➕ Add: %s (category=chroma-demo)\n", demoDoc.ID)

	queryVec, err := emb.GetEmbedding(ctx, "open-source vector database")
	if err != nil {
		log.Fatalf("Failed to embed query: %v", err)
	}
	vecRes, err := vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     queryVec,
		Limit:      3,
	})
	if err != nil {
		log.Fatalf("Vector search failed: %v", err)
	}
	fmt.Printf("  🔎 Vector search (top %d):\n", len(vecRes.Results))
	for _, hit := range vecRes.Results {
		fmt.Printf("    - %s score=%.4f\n", hit.Document.ID, hit.Score)
	}

	filterRes, err := vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"category": "chroma-demo"},
		},
	})
	if err != nil {
		log.Fatalf("Filter search failed: %v", err)
	}
	fmt.Printf("  🔎 Filter search (category=chroma-demo, %d hit):\n", len(filterRes.Results))
	for _, hit := range filterRes.Results {
		fmt.Printf("    - %s content=%q\n", hit.Document.ID, hit.Document.Content)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"chroma-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"chroma-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test query
	fmt.Println("\n🔍 Querying knowledge from Chroma...")
	eventChan, err := r.Run(ctx, "user", "session-1",
		model.NewUserMessage("What are Large Language Models?"))
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	fmt.Print("🤖 Response: ")
	for evt := range eventChan {
		util.PrintEventWithToolCalls(evt)
		if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
			fmt.Println(evt.Response.Choices[0].Message.Content)
		}
	}

	fmt.Println("\n✅ Data persisted in Chroma! Run again to reuse stored embeddings.")
}
