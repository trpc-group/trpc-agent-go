//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using auto source for proto files with AST-based parsing.
//
// This example shows how .proto files are automatically detected and parsed with
// trpc_ast_* metadata extraction for enhanced knowledge retrieval.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint, defaults to https://api.openai.com/v1
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-chat
//	go run main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/proto"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	defaultModelName = "deepseek-chat"
	vectorStore      = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|tcvector|elasticsearch")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	modelName := util.GetEnvOrDefault("MODEL_NAME", defaultModelName)
	fmt.Println("🔮 AST-based Proto File Source Demo")
	fmt.Println("====================================")

	protoFile := util.ExampleDataPath("file/api.proto")

	// Step 1: Demonstrate raw chunking and metadata extraction
	fmt.Println("\n📄 Step 1: Proto File Chunking Preview")
	fmt.Println("----------------------------------------")
	demonstrateChunkingAndMetadata(protoFile)

	// Step 2: Create knowledge base with auto source
	fmt.Println("\n📚 Step 2: Knowledge Base Integration")
	fmt.Println("--------------------------------------")

	src := auto.New(
		[]string{protoFile},
		auto.WithName("Proto API Documentation"),
		auto.WithMetadataValue("source_type", "ast-proto"),
	)

	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources([]source.Source{src}),
	)

	fmt.Println("\n📥 Loading proto file with AST-based parsing...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Step 3: Create agent and test queries
	fmt.Println("\n🔍 Step 3: Semantic Search with LLM")
	fmt.Println("------------------------------------")

	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)
	agent := llmagent.New(
		"proto-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	r := runner.NewRunner(
		"proto-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	queries := []string{
		"What services are defined in the API?",
		"Tell me about the AgentRequest message structure",
		"What RPC methods are available for AgentService and KnowledgeService?",
	}

	for i, q := range queries {
		fmt.Printf("\n%d. 🔍 Query: %s\n", i+1, q)
		eventChan, err := r.Run(ctx, "user", fmt.Sprintf("session-%d", i),
			model.NewUserMessage(q))
		if err != nil {
			log.Printf("Query failed: %v", err)
			continue
		}

		fmt.Print("   🤖 Response: ")
		for evt := range eventChan {
			util.PrintEventWithToolCalls(evt)
			if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
				fmt.Println(evt.Response.Choices[0].Message.Content)
			}
		}
	}

	fmt.Println("\n✅ Demo completed!")
	fmt.Println("\nKey features demonstrated:")
	fmt.Println("- .proto files are automatically detected by extension")
	fmt.Println("- AST-based parsing extracts structured metadata")
	fmt.Println("- Metadata has two groups:")
	fmt.Println("  * trpc_agent_go_*: framework/common metadata (source, uri, chunk info)")
	fmt.Println("  * trpc_ast_*: proto AST semantic metadata (entity type/name/signature/package)")
	fmt.Println("- Search tool result does NOT return all metadata fields; only key metadata is returned")
	fmt.Println("- Knowledge search can query services, messages, and RPC methods")
}

// demonstrateChunkingAndMetadata shows chunking results and extracted metadata
func demonstrateChunkingAndMetadata(protoFile string) {
	reader := proto.New()
	docs, err := reader.ReadFromFile(protoFile)
	if err != nil {
		log.Printf("Warning: Could not parse proto file: %v", err)
		return
	}

	fmt.Printf("✓ Proto file parsed successfully!\n")
	fmt.Printf("✓ Total chunks: %d\n", len(docs))

	// Show all chunks with full content, metadata, and embedding text
	for i, doc := range docs {
		fmt.Printf("\n--- Chunk %d/%d ---\n", i+1, len(docs))
		fmt.Printf("Content (len=%d):\n%s\n", len(doc.Content), doc.Content)

		fmt.Println("Metadata (grouped):")
		printMetadataByPrefix(doc.Metadata)

		// Show the embedding text (metadata JSON used for embedding)
		if doc.EmbeddingText != "" {
			fmt.Printf("\nEmbedding Text (len=%d):\n%s\n", len(doc.EmbeddingText), doc.EmbeddingText)
		}
	}
}

func printMetadataByPrefix(metadata map[string]any) {
	var frameworkKeys []string
	var astKeys []string
	var otherKeys []string

	for key := range metadata {
		switch {
		case strings.HasPrefix(key, "trpc_agent_go_"):
			frameworkKeys = append(frameworkKeys, key)
		case strings.HasPrefix(key, "trpc_ast_"):
			astKeys = append(astKeys, key)
		default:
			otherKeys = append(otherKeys, key)
		}
	}

	sort.Strings(frameworkKeys)
	sort.Strings(astKeys)
	sort.Strings(otherKeys)

	if len(frameworkKeys) > 0 {
		fmt.Println("  trpc_agent_go_* (framework/common):")
		for _, key := range frameworkKeys {
			fmt.Printf("    %s: %v\n", key, metadata[key])
		}
	}

	if len(astKeys) > 0 {
		fmt.Println("  trpc_ast_* (AST semantic):")
		for _, key := range astKeys {
			fmt.Printf("    %s: %v\n", key, metadata[key])
		}
	}

	if len(otherKeys) > 0 {
		fmt.Println("  other metadata:")
		for _, key := range otherKeys {
			fmt.Printf("    %s: %v\n", key, metadata[key])
		}
	}
}
