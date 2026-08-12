//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates opt-in batched embedding ingestion.
//
// It loads the same documents twice, once on the default per-document path
// and once with knowledge.WithEmbeddingBatchSize, and reports how many
// embedding requests each load issued. The embedder is wrapped in a counter
// because the guaranteed effect of batching is a lower request count, not a
// different document set or different vectors.
//
// Required environment variables:
//   - OPENAI_API_KEY: API key for the embedding provider
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI-compatible endpoint
//   - EMBEDDING_MODEL_NAME: (Optional) Embedding model, defaults to
//     text-embedding-3-small
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	go run . -batch-size 16
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

const defaultEmbeddingModel = "text-embedding-3-small"

var (
	batchSize = flag.Int("batch-size", 8,
		"Maximum documents per embedding request; must be at least 2")
	input = flag.String("input", util.ExampleDataPath("file/llm.md"),
		"Local file to index")
	showFallback = flag.Bool("show-fallback", false,
		"Add a load with an embedder that has no batch support, which costs one more request per document")
)

// runResult records what one load reported.
type runResult struct {
	name    string
	counter *requestCounter
	elapsed time.Duration
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	if *batchSize < 2 {
		return fmt.Errorf("-batch-size must be at least 2, got %d", *batchSize)
	}
	modelName := util.GetEnvOrDefault("EMBEDDING_MODEL_NAME", defaultEmbeddingModel)

	fmt.Println("Batch Embedding Demo")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Embedding model: %s\n", modelName)
	fmt.Printf("Input:           %s\n", *input)
	fmt.Printf("Batch size:      %d\n", *batchSize)
	fmt.Println()

	// Every load reads the same source, so the variants differ only in how
	// their documents are grouped into embedding requests.
	src := file.New([]string{*input}, file.WithName("Batch Embedding Example"))
	sources := []source.Source{src}

	results := make([]runResult, 0, 3)

	fmt.Println("Loading with one request per document...")
	perDocument := newCountingEmbedder(openai.New(openai.WithModel(modelName)))
	elapsed, err := load(ctx, perDocument, sources)
	if err != nil {
		return fmt.Errorf("per-document load: %w", err)
	}
	results = append(results, runResult{
		name:    "per-document",
		counter: &perDocument.requestCounter,
		elapsed: elapsed,
	})

	fmt.Printf("Loading with up to %d documents per request...\n", *batchSize)
	batched := newCountingEmbedder(openai.New(openai.WithModel(modelName)))
	elapsed, err = load(ctx, batched, sources, knowledge.WithEmbeddingBatchSize(*batchSize))
	if err != nil {
		return fmt.Errorf("batched load: %w", err)
	}
	results = append(results, runResult{
		name:    fmt.Sprintf("batched (size %d)", *batchSize),
		counter: &batched.requestCounter,
		elapsed: elapsed,
	})

	if *showFallback {
		fmt.Println("Loading with an embedder that has no batch support...")
		fallback := newPerDocumentEmbedder(openai.New(openai.WithModel(modelName)))
		elapsed, err = load(ctx, fallback, sources, knowledge.WithEmbeddingBatchSize(*batchSize))
		if err != nil {
			return fmt.Errorf("fallback load: %w", err)
		}
		results = append(results, runResult{
			name:    "no batch support",
			counter: &fallback.requestCounter,
			elapsed: elapsed,
		})
	}

	report(results, *batchSize)
	return nil
}

// load indexes the sources into a fresh in-memory store and returns how long
// the load took. Progress and statistics output is turned off to keep the
// comparison readable; the log about an ignored batch size is separate from it
// and still appears.
func load(
	ctx context.Context,
	emb embedder.Embedder,
	sources []source.Source,
	opts ...knowledge.LoadOption,
) (time.Duration, error) {
	kb := knowledge.New(
		knowledge.WithVectorStore(inmemory.New()),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources(sources),
	)
	opts = append(opts,
		knowledge.WithShowProgress(false),
		knowledge.WithShowStats(false),
	)
	start := time.Now()
	if err := kb.Load(ctx, opts...); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// report prints the comparison table. It expects the per-document run first
// and the batched run second, as produced by run.
func report(results []runResult, batchSize int) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w,
		"variant\tdocuments\tper-document requests\tbatch requests\ttotal requests\telapsed")
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%s\n",
			r.name,
			r.counter.embeddedTexts.Load(),
			r.counter.singleRequests.Load(),
			r.counter.batchRequests.Load(),
			r.counter.totalRequests(),
			r.elapsed.Round(time.Millisecond),
		)
	}
	_ = w.Flush()

	documents := results[0].counter.embeddedTexts.Load()
	expected := (documents + int64(batchSize) - 1) / int64(batchSize)
	fmt.Println()
	fmt.Printf("Embedding requests for %d documents: %d without batching, %d with batching (ceil(%d/%d) = %d).\n",
		documents,
		results[0].counter.totalRequests(),
		results[1].counter.totalRequests(),
		documents, batchSize, expected,
	)
	fmt.Println("Elapsed is one sample from one run against one provider, not a benchmark.")
	fmt.Println("Batching changes how many requests carry the documents; it does not change")
	fmt.Println("the documents, the model, or the number of vector store writes.")
}
