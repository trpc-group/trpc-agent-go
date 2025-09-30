//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package knowledge

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/query"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/retriever"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Option represents a functional option for configuring BuiltinKnowledge.
type Option func(*BuiltinKnowledge)

// WithVectorStore sets the vector store for similarity search.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(dk *BuiltinKnowledge) {
		dk.vectorStore = vs
	}
}

// WithEmbedder sets the embedder for generating document embeddings.
func WithEmbedder(e embedder.Embedder) Option {
	return func(dk *BuiltinKnowledge) {
		dk.embedder = e
	}
}

// WithEnableSourceSync sets the enable source sync.
func WithEnableSourceSync(enable bool) Option {
	return func(dk *BuiltinKnowledge) {
		dk.enableSourceSync = enable
	}
}

// WithQueryEnhancer sets a custom query enhancer (optional).
func WithQueryEnhancer(qe query.Enhancer) Option {
	return func(dk *BuiltinKnowledge) {
		dk.queryEnhancer = qe
	}
}

// WithReranker sets a custom reranker (optional).
func WithReranker(r reranker.Reranker) Option {
	return func(dk *BuiltinKnowledge) {
		dk.reranker = r
	}
}

// WithRetriever sets a custom retriever (optional).
func WithRetriever(r retriever.Retriever) Option {
	return func(dk *BuiltinKnowledge) {
		dk.retriever = r
	}
}

// WithSources sets the knowledge sources.
func WithSources(sources []source.Source) Option {
	return func(dk *BuiltinKnowledge) {
		dk.sources = sources
	}
}

// loadConfig holds the configuration for load behavior.
type loadConfig struct {
	showProgress     bool
	progressStepSize int
	showStats        bool
	srcParallelism   int
	docParallelism   int
	recreate         bool
}

// LoadOption represents a functional option for configuring load behavior.
type LoadOption func(*loadConfig)

// WithShowProgress enables or disables progress logging during load.
func WithShowProgress(show bool) LoadOption {
	return func(lc *loadConfig) {
		lc.showProgress = show
	}
}

// WithProgressStepSize sets the granularity of progress updates.
func WithProgressStepSize(stepSize int) LoadOption {
	return func(lc *loadConfig) {
		lc.progressStepSize = stepSize
	}
}

// WithShowStats enables or disables statistics logging during load.
// By default statistics are shown.
func WithShowStats(show bool) LoadOption {
	return func(lc *loadConfig) {
		lc.showStats = show
	}
}

// WithSourceConcurrency configures how many sources can be loaded in parallel.
// A value = 1 means sequential processing.
// The default is min(4, len(sources)) when value is not specified (=0).
func WithSourceConcurrency(n int) LoadOption {
	return func(lc *loadConfig) {
		lc.srcParallelism = n
	}
}

// WithDocConcurrency configures how many documents per source can be processed
// concurrently.
// A value = 1 means sequential processing.
// The default is runtime.NumCPU() when value is not specified (=0).
func WithDocConcurrency(n int) LoadOption {
	return func(lc *loadConfig) {
		lc.docParallelism = n
	}
}

// WithRecreate recreates the vector store before loading documents, be careful to use this option.
// ATTENTION! This option will delete all documents from the vector store and recreate it.
func WithRecreate(recreate bool) LoadOption {
	return func(lc *loadConfig) {
		lc.recreate = recreate
	}
}

type showDocumentInfoConfig struct {
	ids        []string
	filter     map[string]interface{}
	sourceName string
}

// ShowDocumentInfoOption represents a functional option for configuring show document info behavior.
type ShowDocumentInfoOption func(*showDocumentInfoConfig)

// WithShowDocumentInfoIDs sets the document ids to show.
func WithShowDocumentInfoIDs(ids []string) ShowDocumentInfoOption {
	return func(s *showDocumentInfoConfig) {
		s.ids = ids
	}
}

// WithShowDocumentInfoFilter sets the filter for the document info.
func WithShowDocumentInfoFilter(filter map[string]interface{}) ShowDocumentInfoOption {
	return func(s *showDocumentInfoConfig) {
		s.filter = filter
	}
}

// WithShowDocumentInfoSourceName sets the source name for the document info.
func WithShowDocumentInfoSourceName(sourceName string) ShowDocumentInfoOption {
	return func(s *showDocumentInfoConfig) {
		s.sourceName = sourceName
	}
}
