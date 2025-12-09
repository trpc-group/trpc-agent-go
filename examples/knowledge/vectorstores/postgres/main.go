//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using PostgreSQL with pgvector for persistent storage.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint, defaults to https://api.openai.com/v1
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat
//   - PGVECTOR_HOST: (Optional) PostgreSQL host, defaults to 127.0.0.1
//   - PGVECTOR_PORT: (Optional) PostgreSQL port, defaults to 5432
//   - PGVECTOR_USER: (Optional) PostgreSQL user, defaults to postgres
//   - PGVECTOR_PASSWORD: (Optional) PostgreSQL password
//   - PGVECTOR_DATABASE: (Optional) PostgreSQL database name, defaults to vectordb
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-chat
//	export PGVECTOR_HOST=127.0.0.1
//	export PGVECTOR_PORT=5432
//	export PGVECTOR_USER=postgres
//	export PGVECTOR_PASSWORD=your-password
//	export PGVECTOR_DATABASE=vectordb
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName = util.GetEnvOrDefault("MODEL_NAME", "deepseek-chat")
	host      = util.GetEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")
	portStr   = util.GetEnvOrDefault("PGVECTOR_PORT", "5432")
	user      = util.GetEnvOrDefault("PGVECTOR_USER", "root")
	password  = util.GetEnvOrDefault("PGVECTOR_PASSWORD", "123")
	database  = util.GetEnvOrDefault("PGVECTOR_DATABASE", "vectordb")
	table     = util.GetEnvOrDefault("PGVECTOR_TABLE", "trpc_example")
)

func main() {
	ctx := context.Background()

	fmt.Println("🐘 PostgreSQL (PGVector) Demo")
	fmt.Println("==============================")

	port, _ := strconv.Atoi(portStr)
	fmt.Printf("📊 Connecting to PostgreSQL: %s:%d/%s table: %s\\n", host, port, database, table)

	// Create PGVector store
	vs, err := pgvector.New(
		pgvector.WithHost(host),
		pgvector.WithPort(port),
		pgvector.WithUser(user),
		pgvector.WithPassword(password),
		pgvector.WithDatabase(database),
		pgvector.WithTable(table),
	)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}

	// Create file source
	src := file.New(
		[]string{util.ExampleDataPath("file/llm.md")},
		file.WithName("LLM Docs"),
	)

	// Create knowledge base
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources([]source.Source{src}),
	)

	fmt.Println("\n📥 Loading knowledge into PostgreSQL...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"pg-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"pg-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test query
	fmt.Println("\n🔍 Querying knowledge from PostgreSQL...")
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

	fmt.Println("\n✅ Data persisted in PostgreSQL! Run again to reuse stored embeddings.")
}
