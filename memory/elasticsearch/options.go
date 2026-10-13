//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"maps"
	"time"

	istorage "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

// Default index and search settings.
const (
	defaultIndexName           = "trpc_agent_memories"
	defaultIndexDimension      = 1536
	defaultMaxResults          = 15
	defaultSimilarityThreshold = 0.30
)

// Default timeout settings.
const (
	defaultIndexInitTimeout = 30 * time.Second
)

var defaultOptions = ServiceOpts{
	indexName:           defaultIndexName,
	indexDimension:      defaultIndexDimension,
	maxResults:          defaultMaxResults,
	similarityThreshold: defaultSimilarityThreshold,
	toolCreators:        imemory.AllToolCreators,
	enabledTools:        imemory.DefaultEnabledTools,
	asyncMemoryNum:      imemory.DefaultAsyncMemoryNum,
}

// ServiceOpts is the options for the Elasticsearch memory service.
type ServiceOpts struct {
	// Elasticsearch connection settings.
	addresses              []string
	username               string
	password               string
	apiKey                 string
	certificateFingerprint string
	compressRequestBody    bool
	enableMetrics          bool
	enableDebugLogger      bool
	retryOnStatus          []int
	maxRetries             int
	extraOptions           []any
	version                storage.ESVersion

	// client allows injecting a pre-built storage client.
	// When set, all other connection settings are ignored.
	client istorage.Client

	// Index and search settings.
	indexName            string
	indexDimension       int
	maxResults           int
	hybridCandidateLimit int
	similarityThreshold  float64
	softDelete           bool
	skipIndexInit        bool

	// Embedder for generating memory embeddings.
	embedder embedder.Embedder

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

	// Async memory worker configuration.
	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
	// disableAutoMemoryOnExternalContext skips auto extraction for polluted sessions.
	disableAutoMemoryOnExternalContext bool
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o

	opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
	for name, toolCreator := range o.toolCreators {
		opts.toolCreators[name] = toolCreator
	}

	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)

	// Initialize userExplicitlySet map (empty for new clone).
	opts.userExplicitlySet = make(map[string]struct{})

	return opts
}

// ServiceOpt is the option for the Elasticsearch memory service.
type ServiceOpt func(*ServiceOpts)

// WithAddresses sets the Elasticsearch node addresses.
func WithAddresses(addresses []string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.addresses = addresses
	}
}

// WithUsername sets the username for authentication.
func WithUsername(username string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.username = username
	}
}

// WithPassword sets the password for authentication.
func WithPassword(password string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.password = password
	}
}

// WithAPIKey sets the API key for authentication.
func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.apiKey = apiKey
	}
}

// WithCertificateFingerprint sets the TLS certificate fingerprint.
func WithCertificateFingerprint(fingerprint string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.certificateFingerprint = fingerprint
	}
}

// WithCompressRequestBody enables request body compression.
func WithCompressRequestBody(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.compressRequestBody = enabled
	}
}

// WithEnableMetrics enables client metrics.
func WithEnableMetrics(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableMetrics = enabled
	}
}

// WithEnableDebugLogger enables the client debug logger.
func WithEnableDebugLogger(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableDebugLogger = enabled
	}
}

// WithRetryOnStatus sets the HTTP status codes to retry on.
func WithRetryOnStatus(codes []int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.retryOnStatus = codes
	}
}

// WithMaxRetries sets the maximum number of client retries.
func WithMaxRetries(n int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.maxRetries = n
	}
}

// WithVersion selects the target Elasticsearch major version (v7, v8 or v9).
// The version controls the index mapping compatibility: Elasticsearch 7
// does not support the index and similarity parameters on dense_vector
// fields. It defaults to v9, matching the storage client default.
func WithVersion(version storage.ESVersion) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.version = version
	}
}

// WithExtraOptions sets extra options passed through to the Elasticsearch
// client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithIndexName sets the name of the index storing memories.
// Default is "trpc_agent_memories".
func WithIndexName(indexName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if indexName != "" {
			opts.indexName = indexName
		}
	}
}

// WithIndexDimension sets the embedding dimension of the dense_vector field.
// Default is 1536.
func WithIndexDimension(dimension int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if dimension > 0 {
			opts.indexDimension = dimension
		}
	}
}

// WithMaxResults sets the maximum number of search results.
// Default is 15.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if maxResults > 0 {
			opts.maxResults = maxResults
		}
	}
}

// WithHybridCandidateLimit sets how many candidates each dense and lexical
// search may return before Reciprocal Rank Fusion in hybrid search mode.
// A value of 0 means use maxResults.
func WithHybridCandidateLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if limit > 0 {
			opts.hybridCandidateLimit = limit
		}
	}
}

// WithSimilarityThreshold sets the minimum similarity threshold for search
// results. Results below this threshold are filtered out.
// Value should be between 0 and 1. A value of 0 disables filtering.
// Default is 0.30.
func WithSimilarityThreshold(threshold float64) ServiceOpt {
	return func(opts *ServiceOpts) {
		if threshold >= 0 && threshold <= 1 {
			opts.similarityThreshold = threshold
		}
	}
}

// WithSoftDelete enables or disables soft delete behavior.
// When enabled, delete operations set deleted_at and queries filter
// tombstoned documents. Default is disabled (hard delete).
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enabled
	}
}

// WithSkipIndexInit skips index existence checks and creation at service
// startup. Useful when the index is managed externally or the user has no
// index administration permissions.
func WithSkipIndexInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipIndexInit = skip
	}
}

// WithEmbedder sets the embedder for generating memory embeddings.
// This is required for vector-based memory search.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.embedder = e
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools,
// but user settings via WithToolEnabled (before or after) take precedence.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = e
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid or creator is nil, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		if opts.toolCreators == nil {
			opts.toolCreators = make(map[string]memory.ToolCreator)
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools via Tools() in auto memory
// mode so the agent can call them directly. Invalid tool names are ignored.
func WithAutoMemoryExposedTools(toolNames ...string) ServiceOpt {
	return func(opts *ServiceOpts) {
		for _, toolName := range toolNames {
			WithToolExposed(toolName, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via
// Tools(). Use WithAutoMemoryExposedTools for the common auto memory case.
func WithToolExposed(toolName string, exposed bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if exposed {
			if opts.toolExposed == nil {
				opts.toolExposed = make(map[string]struct{})
			}
			opts.toolExposed[toolName] = struct{}{}
			delete(opts.toolHidden, toolName)
			return
		}
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		opts.toolHidden[toolName] = struct{}{}
		delete(opts.toolExposed, toolName)
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
// User settings via WithToolEnabled take precedence over auto mode
// defaults, regardless of option order.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for memory jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for each memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryJobTimeout = timeout
	}
}

// WithDisableAutoMemoryOnExternalContext stops future automatic memory
// extraction for sessions that consumed framework-owned external context.
func WithDisableAutoMemoryOnExternalContext(disable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.disableAutoMemoryOnExternalContext = disable
	}
}

// withClient injects a pre-built storage client.
// It is intended for tests running in this package.
func withClient(client istorage.Client) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.client = client
	}
}
