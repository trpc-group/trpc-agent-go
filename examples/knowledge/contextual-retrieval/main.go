//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates opt-in, index-time contextual retrieval.
//
// The contextual variant asks a model to create a short context for each chunk,
// prepends that context only to Document.EmbeddingText, and keeps
// Document.Content unchanged for retrieval results and the Agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	knowledgeutil "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	embedderopenai "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultAnswerModel    = "deepseek-v4-flash"
	defaultEmbeddingModel = "text-embedding-3-small"
	defaultQuery          = "What are Large Language Models and how do they work?"
	defaultContextCache   = ".context-cache/contexts.jsonl"
	defaultContextWorkers = 4
	defaultChunkSize      = 500
	defaultChunkOverlap   = 50
)

var (
	indexVariant = flag.String(
		"index-variant",
		indexVariantBaseline,
		"Index text variant: baseline|contextual",
	)
	inputFiles = flag.String(
		"input",
		knowledgeutil.ExampleDataPath("file/llm.md"),
		"Comma-separated local .md or .txt files",
	)
	query       = flag.String("query", defaultQuery, "Question to ask the knowledge Agent")
	answerModel = flag.String(
		"model",
		knowledgeutil.GetEnvOrDefault("MODEL_NAME", defaultAnswerModel),
		"OpenAI-compatible model used by the Agent",
	)
	contextModel = flag.String(
		"context-model",
		knowledgeutil.GetEnvOrDefault(
			"CONTEXT_MODEL_NAME",
			knowledgeutil.GetEnvOrDefault("MODEL_NAME", defaultAnswerModel),
		),
		"OpenAI-compatible model used to generate chunk contexts",
	)
	contextProviderID = flag.String(
		"context-provider-id",
		knowledgeutil.GetEnvOrDefault("CONTEXT_PROVIDER_ID", ""),
		"Optional stable context-provider deployment ID; hashed before it is stored in index/cache identity",
	)
	embeddingModel = flag.String(
		"embedding-model",
		knowledgeutil.GetEnvOrDefault("EMBEDDING_MODEL_NAME", defaultEmbeddingModel),
		"OpenAI-compatible embedding model",
	)
	contextCachePath = flag.String(
		"context-cache",
		defaultContextCache,
		"Append-only JSONL cache for generated contexts",
	)
	contextWorkers = flag.Int(
		"context-workers",
		defaultContextWorkers,
		"Maximum concurrent context-generation calls",
	)
	contextCacheOnly = flag.Bool(
		"context-cache-only",
		false,
		"Require every contextual chunk to be present in the cache; make no context-model calls",
	)
	vectorStore = flag.String(
		"vectorstore",
		"inmemory",
		"Vector store: inmemory|sqlitevec|pgvector|tcvector|elasticsearch|milvus",
	)
	indexNamespace = flag.String(
		"index-namespace",
		"",
		"Required prefix for a dedicated persistent index; the variant suffix is added automatically",
	)
	chunkSize    = flag.Int("chunk-size", defaultChunkSize, "Chunk size passed to the file source")
	chunkOverlap = flag.Int(
		"chunk-overlap",
		defaultChunkOverlap,
		"Chunk overlap passed to the file source",
	)
)

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	if *indexVariant != indexVariantBaseline && *indexVariant != indexVariantContextual {
		return fmt.Errorf("invalid -index-variant %q", *indexVariant)
	}
	if strings.TrimSpace(*query) == "" {
		return errors.New("-query must not be empty")
	}
	if *chunkSize <= 0 {
		return errors.New("-chunk-size must be greater than zero")
	}
	if *chunkOverlap < 0 || *chunkOverlap >= *chunkSize {
		return errors.New("-chunk-overlap must be non-negative and smaller than -chunk-size")
	}
	if *contextCacheOnly && *indexVariant != indexVariantContextual {
		return errors.New("-context-cache-only is valid only with -index-variant contextual")
	}

	paths, err := parseInputPaths(*inputFiles)
	if err != nil {
		return err
	}
	storeType := knowledgeutil.VectorStoreType(*vectorStore)
	resolvedNamespace, err := configureVectorStoreNamespace(
		storeType,
		*indexNamespace,
		*indexVariant,
	)
	if err != nil {
		return err
	}

	baseSource := filesource.New(
		paths,
		filesource.WithName("Contextual Retrieval Documents"),
		filesource.WithChunkSize(*chunkSize),
		filesource.WithChunkOverlap(*chunkOverlap),
	)

	sourceConfig := contextualSourceConfig{
		Delegate:                   baseSource,
		Variant:                    *indexVariant,
		Workers:                    *contextWorkers,
		PromptVersion:              contextPromptVersionV1,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
	}
	if *indexVariant == indexVariantContextual {
		resolver, err := newLocalFileParentResolver(paths)
		if err != nil {
			return fmt.Errorf("configure parent resolver: %w", err)
		}
		cache, err := openContextCache(*contextCachePath)
		if err != nil {
			return fmt.Errorf("open context cache: %w", err)
		}

		var contextLLM model.Model
		if !*contextCacheOnly {
			contextLLM = openaimodel.New(*contextModel)
		}
		provider, err := newModelContextProvider(
			contextLLM,
			*contextModel,
			*contextProviderID,
		)
		if err != nil {
			return fmt.Errorf("configure context provider: %w", err)
		}
		sourceConfig.Provider = provider
		sourceConfig.Resolver = resolver
		sourceConfig.Cache = cache
		sourceConfig.CacheOnly = *contextCacheOnly
	}

	indexSource, err := newContextualSource(sourceConfig)
	if err != nil {
		return fmt.Errorf("configure index source: %w", err)
	}
	vs, err := knowledgeutil.NewVectorStoreByType(storeType)
	if err != nil {
		return fmt.Errorf("create vector store: %w", err)
	}

	fmt.Println("Contextual Retrieval Knowledge Example")
	fmt.Printf("Index variant: %s\n", *indexVariant)
	fmt.Printf("Vector store: %s\n", storeType)
	if resolvedNamespace != "" {
		fmt.Printf("Index namespace: %s\n", resolvedNamespace)
	}
	if *indexVariant == indexVariantContextual {
		fmt.Printf("Context model: %s\n", *contextModel)
		fmt.Printf("Context cache-only: %t\n", *contextCacheOnly)
	}

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(embedderopenai.New(
			embedderopenai.WithModel(*embeddingModel),
		)),
		knowledge.WithSources([]source.Source{indexSource}),
		knowledge.WithEnableSourceSync(true),
	)
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		return fmt.Errorf("load knowledge: %w", err)
	}
	knowledgeutil.WaitForIndexRefresh(storeType)

	searchTool := knowledgetool.NewKnowledgeSearchTool(
		vectorOnlyKnowledge{Knowledge: kb},
		knowledgetool.WithMaxResults(3),
	)
	answerTemperature := 0.0
	agent := llmagent.New(
		"contextual-retrieval-assistant",
		llmagent.WithModel(openaimodel.New(*answerModel)),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Temperature: &answerTemperature,
		}),
		llmagent.WithInstruction(
			"Use the knowledge search tool to answer the user from the indexed documents. "+
				"Do not claim facts that the retrieved documents do not support.",
		),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)
	r := runner.NewRunner(
		"contextual-retrieval-example",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer func() {
		_ = r.Close()
	}()

	fmt.Printf("\nQuery: %s\n", *query)
	events, err := r.Run(ctx, "example-user", "example-session", model.NewUserMessage(*query))
	if err != nil {
		return fmt.Errorf("run Agent: %w", err)
	}

	var streamedAnswer strings.Builder
	for event := range events {
		if event == nil || event.Response == nil {
			continue
		}
		knowledgeutil.PrintEventWithToolCalls(event)
		if event.Error != nil {
			return fmt.Errorf("agent response error: %w", event.Error)
		}
		if len(event.Choices) == 0 {
			continue
		}
		choice := event.Choices[0]
		if choice.Delta.Content != "" {
			streamedAnswer.WriteString(choice.Delta.Content)
		}
		if event.IsFinalResponse() {
			answer := strings.TrimSpace(streamedAnswer.String())
			if answer == "" {
				answer = strings.TrimSpace(choice.Message.Content)
			}
			if answer != "" {
				fmt.Printf("\nFinal answer:\n%s\n", answer)
			}
		}
	}
	return nil
}

type vectorOnlyKnowledge struct {
	knowledge.Knowledge
}

func (k vectorOnlyKnowledge) Search(
	ctx context.Context,
	request *knowledge.SearchRequest,
) (*knowledge.SearchResult, error) {
	if request == nil {
		return k.Knowledge.Search(ctx, nil)
	}
	requestCopy := *request
	requestCopy.SearchMode = vectorstore.SearchModeVector
	return k.Knowledge.Search(ctx, &requestCopy)
}

func parseInputPaths(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	var paths []string
	for _, item := range strings.Split(raw, ",") {
		path, err := normalizeSupportedTextPath(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, errors.New("-input must contain at least one local .md or .txt file")
	}
	return paths, nil
}

func configureVectorStoreNamespace(
	storeType knowledgeutil.VectorStoreType,
	prefix string,
	variant string,
) (string, error) {
	if storeType == knowledgeutil.VectorStoreInMemory {
		return "", nil
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", errors.New("-index-namespace is required for persistent vector stores")
	}
	if err := validateIndexNamespace(prefix); err != nil {
		return "", err
	}
	namespace := prefix + "_" + variant

	var environment map[string]string
	switch storeType {
	case knowledgeutil.VectorStorePGVector:
		environment = map[string]string{"PGVECTOR_TABLE": namespace}
	case knowledgeutil.VectorStoreSQLiteVec:
		environment = map[string]string{
			"SQLITEVEC_TABLE":          namespace + "_documents",
			"SQLITEVEC_METADATA_TABLE": namespace + "_metadata",
		}
	case knowledgeutil.VectorStoreTCVector:
		environment = map[string]string{"TCVECTOR_COLLECTION": namespace}
	case knowledgeutil.VectorStoreElasticsearch:
		environment = map[string]string{"ELASTICSEARCH_INDEX_NAME": namespace}
	case knowledgeutil.VectorStoreMilvus:
		environment = map[string]string{"MILVUS_COLLECTION": namespace}
	default:
		return "", fmt.Errorf("unsupported -vectorstore %q", storeType)
	}
	for key, value := range environment {
		if err := os.Setenv(key, value); err != nil {
			return "", fmt.Errorf("configure %s: %w", key, err)
		}
	}
	return namespace, nil
}

func validateIndexNamespace(namespace string) error {
	if len(namespace) > 40 {
		return errors.New("-index-namespace must be at most 40 characters")
	}
	for index := 0; index < len(namespace); index++ {
		character := namespace[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			(character == '_' && index > 0) {
			continue
		}
		return errors.New("-index-namespace must use lowercase letters, digits, and underscores")
	}
	if len(namespace) == 0 || namespace[0] < 'a' || namespace[0] > 'z' {
		return errors.New("-index-namespace must start with a lowercase letter")
	}
	return nil
}
