//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates opt-in, index-time contextual retrieval.
//
// The contextual variant changes only Document.EmbeddingText. Retrieved
// Document.Content remains the original chunk shown to the Agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	knowledgeutil "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/examples/knowledge/contextual-retrieval/internal/contextual"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	embedderopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	variantBaseline       = "baseline"
	variantContextual     = "contextual"
	defaultAnswerModel    = "deepseek-v4-flash"
	defaultEmbeddingModel = "text-embedding-3-small"
	defaultQuery          = "What are Large Language Models and how do they work?"
	defaultContextCache   = ".context-cache/contexts.json"
	defaultChunkSize      = 500
	defaultChunkOverlap   = 50
)

var (
	indexVariant = flag.String("index-variant", variantBaseline, "Index variant: baseline|contextual")
	inputPath    = flag.String(
		"input",
		knowledgeutil.ExampleDataPath("file/llm.md"),
		"One local UTF-8 text file to index",
	)
	query = flag.String("query", defaultQuery, "Question to ask the knowledge Agent")
	cache = flag.String(
		"context-cache",
		defaultContextCache,
		"Local cache used only by the contextual index variant",
	)
	contextMaxPromptBytes = flag.Int64(
		"context-max-prompt-bytes",
		contextual.DefaultMaxPromptBytes,
		"Maximum cumulative prompt bytes for uncached contextual chunks",
	)
)

type config struct {
	variant               string
	inputPath             string
	query                 string
	cachePath             string
	contextMaxPromptBytes int64
}

func main() {
	flag.Parse()
	if err := run(context.Background(), config{
		variant:               *indexVariant,
		inputPath:             *inputPath,
		query:                 *query,
		cachePath:             *cache,
		contextMaxPromptBytes: *contextMaxPromptBytes,
	}); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config) error {
	cfg.inputPath = strings.TrimSpace(cfg.inputPath)
	cfg.query = strings.TrimSpace(cfg.query)
	if cfg.variant != variantBaseline && cfg.variant != variantContextual {
		return fmt.Errorf("invalid -index-variant %q", cfg.variant)
	}
	if cfg.inputPath == "" {
		return errors.New("-input must not be empty")
	}
	if cfg.query == "" {
		return errors.New("-query must not be empty")
	}
	baseSource := filesource.New(
		[]string{cfg.inputPath},
		filesource.WithName("Contextual Retrieval Document"),
		filesource.WithChunkSize(defaultChunkSize),
		filesource.WithChunkOverlap(defaultChunkOverlap),
	)
	var indexSource source.Source = baseSource
	if cfg.variant == variantContextual {
		parentText, err := readParentDocument(cfg.inputPath)
		if err != nil {
			return err
		}
		contextModelName := knowledgeutil.GetEnvOrDefault(
			"CONTEXT_MODEL_NAME",
			knowledgeutil.GetEnvOrDefault("MODEL_NAME", defaultAnswerModel),
		)
		indexSource, err = contextual.NewSource(baseSource, contextual.SourceConfig{
			ParentText:     parentText,
			Model:          openaimodel.New(contextModelName),
			ModelName:      contextModelName,
			ModelEndpoint:  os.Getenv("OPENAI_BASE_URL"),
			CachePath:      cfg.cachePath,
			MaxPromptBytes: cfg.contextMaxPromptBytes,
		})
		if err != nil {
			return fmt.Errorf("configure contextual source: %w", err)
		}
	}

	kb := knowledge.New(
		knowledge.WithVectorStore(inmemory.New()),
		knowledge.WithEmbedder(embedderopenai.New(
			embedderopenai.WithModel(knowledgeutil.GetEnvOrDefault(
				"EMBEDDING_MODEL_NAME",
				defaultEmbeddingModel,
			)),
		)),
		knowledge.WithSources([]source.Source{indexSource}),
	)
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		return fmt.Errorf("load knowledge: %w", err)
	}

	searchTool := knowledgetool.NewKnowledgeSearchTool(
		contextual.VectorOnly(kb),
		knowledgetool.WithMaxResults(3),
	)
	temperature := 0.0
	agent := llmagent.New(
		"contextual-retrieval-assistant",
		llmagent.WithModel(openaimodel.New(knowledgeutil.GetEnvOrDefault(
			"MODEL_NAME",
			defaultAnswerModel,
		))),
		llmagent.WithGenerationConfig(model.GenerationConfig{Temperature: &temperature}),
		llmagent.WithInstruction(
			"Use the knowledge search tool to answer from the indexed document. "+
				"Do not claim facts that the retrieved document does not support.",
		),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)
	return runQuery(ctx, agent, cfg)
}

func runQuery(ctx context.Context, agent *llmagent.LLMAgent, cfg config) error {
	r := runner.NewRunner(
		"contextual-retrieval-example",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer func() {
		_ = r.Close()
	}()

	fmt.Printf("Index variant: %s\nQuery: %s\n", cfg.variant, cfg.query)
	events, err := r.Run(ctx, "example-user", "example-session", model.NewUserMessage(cfg.query))
	if err != nil {
		return fmt.Errorf("run Agent: %w", err)
	}
	for event := range events {
		if event == nil || event.Response == nil {
			continue
		}
		knowledgeutil.PrintEventWithToolCalls(event)
		if event.Error != nil {
			return fmt.Errorf("agent response error: %w", event.Error)
		}
	}
	return nil
}

func readParentDocument(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read parent document: %w", err)
	}
	if !utf8.Valid(content) {
		return "", errors.New("parent document is not valid UTF-8 text")
	}
	if strings.TrimSpace(string(content)) == "" {
		return "", errors.New("parent document is empty")
	}
	return string(content), nil
}
