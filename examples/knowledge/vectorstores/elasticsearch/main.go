//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using Elasticsearch for vector storage.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint, defaults to https://api.openai.com/v1
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat
//   - ELASTICSEARCH_HOSTS: (Optional) Elasticsearch hosts, defaults to http://localhost:9200
//   - ELASTICSEARCH_USERNAME: (Optional) Elasticsearch username
//   - ELASTICSEARCH_PASSWORD: (Optional) Elasticsearch password
//   - ELASTICSEARCH_API_KEY: (Optional) Elasticsearch API key (alternative to username/password)
//   - ELASTICSEARCH_INDEX_NAME: (Optional) Index name, defaults to trpc_agent_go
//   - ELASTICSEARCH_VERSION: (Optional) Elasticsearch version (v7, v8, v9), defaults to v9
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-chat
//	export ELASTICSEARCH_HOSTS=http://localhost:9200
//	export ELASTICSEARCH_USERNAME=elastic
//	export ELASTICSEARCH_PASSWORD=your-password
//	go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName = util.GetEnvOrDefault("MODEL_NAME", "deepseek-chat")
	hosts     = util.GetEnvOrDefault("ELASTICSEARCH_HOSTS", "http://localhost:9200")
	username  = util.GetEnvOrDefault("ELASTICSEARCH_USERNAME", "")
	password  = util.GetEnvOrDefault("ELASTICSEARCH_PASSWORD", "")
	indexName = util.GetEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_go")
	version   = util.GetEnvOrDefault("ELASTICSEARCH_VERSION", "v9")
)

func main() {
	ctx := context.Background()

	fmt.Println("🔍 Elasticsearch Vector Store Demo")
	fmt.Println("===================================")

	fmt.Printf("📊 Elasticsearch: %s (Index: %s, Version: %s)\\n", hosts, indexName, version)

	// Custom document builder
	docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
		var source struct {
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Content   string         `json:"content"`
			CreatedAt time.Time      `json:"created_at"`
			UpdatedAt time.Time      `json:"updated_at"`
			Embedding []float64      `json:"embedding"`
			Metadata  map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(hitSource, &source); err != nil {
			return nil, nil, err
		}
		doc := &document.Document{
			ID:        source.ID,
			Name:      source.Name,
			Content:   source.Content,
			CreatedAt: source.CreatedAt,
			UpdatedAt: source.UpdatedAt,
			Metadata:  source.Metadata,
		}
		return doc, source.Embedding, nil
	}

	hostList := strings.Split(hosts, ",")
	vs, err := elasticsearch.New(
		elasticsearch.WithAddresses(hostList),
		elasticsearch.WithUsername(username),
		elasticsearch.WithPassword(password),
		elasticsearch.WithIndexName(indexName),
		elasticsearch.WithVersion(version),
		elasticsearch.WithMaxRetries(3),
		elasticsearch.WithDocBuilder(docBuilder),
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

	fmt.Println("\n📥 Indexing knowledge into Elasticsearch...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"es-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner("es-chat", agent)
	defer r.Close()

	// Test query
	fmt.Println("\n🔍 Searching Elasticsearch index...")
	eventChan, err := r.Run(ctx, "user", "session-1",
		model.NewUserMessage("What are transformers in machine learning?"))
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

	fmt.Println("\n✅ Data indexed in Elasticsearch!")
}
