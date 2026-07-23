//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptionsDefaults(t *testing.T) {
	opts := defaultServiceOpts()

	assert.Equal(t, defaultCollectionName, opts.collectionName)
	assert.True(t, opts.autoCreateCollection)
	assert.Equal(t, defaultMaxResults, opts.maxResults)
	assert.InDelta(t, defaultSimilarityThreshold, opts.similarityThreshold, 0.0001)
	assert.Equal(t, 1000, opts.memoryLimit)
	assert.Equal(t, defaultHybridCandidateLimit, opts.hybridCandidateLimit)
	assert.Equal(t, defaultRequestTimeout, opts.timeout)
}

func TestOptionsDefaultsDoNotShareMaps(t *testing.T) {
	first := defaultServiceOpts()
	second := defaultServiceOpts()

	first.headers["X-Test"] = "value"
	first.enabledTools["test-only"] = struct{}{}
	first.toolExposed["search_memory"] = struct{}{}

	assert.Empty(t, second.headers)
	assert.NotContains(t, second.enabledTools, "test-only")
	assert.Empty(t, second.toolExposed)
}

func TestOptionsLastValueWins(t *testing.T) {
	opts := defaultServiceOpts()
	WithSimilarityThreshold(0.2)(&opts)
	WithSimilarityThreshold(0.8)(&opts)
	WithMaxResults(3)(&opts)
	WithMaxResults(7)(&opts)
	WithIndexDimension(2)(&opts)
	WithIndexDimension(3)(&opts)
	WithHTTPHeaders(map[string]string{"X-First": "first"})(&opts)
	WithHTTPHeaders(map[string]string{"X-Last": "last"})(&opts)

	assert.InDelta(t, 0.8, opts.similarityThreshold, 0.0001)
	assert.Equal(t, 7, opts.maxResults)
	require.NotNil(t, opts.indexDimension)
	assert.Equal(t, 3, *opts.indexDimension)
	assert.Equal(t, map[string]string{"X-Last": "last"}, opts.headers)
}

func TestOptionsHTTPHeadersAreCopied(t *testing.T) {
	headers := map[string]string{"X-Custom": "before"}
	option := WithHTTPHeaders(headers)
	headers["X-Custom"] = "after"
	headers["X-Added"] = "value"
	opts := defaultServiceOpts()
	option(&opts)

	headers["X-Custom"] = "later"
	opts.headers["X-Custom"] = "service"
	second := defaultServiceOpts()
	option(&second)

	assert.Equal(t, "service", opts.headers["X-Custom"])
	assert.Equal(t, "before", second.headers["X-Custom"])
	assert.NotContains(t, second.headers, "X-Added")
}

func TestOptionsIgnoreNonPositiveWorkerValues(t *testing.T) {
	opts := defaultServiceOpts()
	workers := opts.asyncMemoryNum
	queueSize := opts.memoryQueueSize

	WithAsyncMemoryNum(0)(&opts)
	WithAsyncMemoryNum(-1)(&opts)
	WithMemoryQueueSize(0)(&opts)
	WithMemoryQueueSize(-1)(&opts)

	assert.Equal(t, workers, opts.asyncMemoryNum)
	assert.Equal(t, queueSize, opts.memoryQueueSize)
}

func TestOptionsRejectInvalidFinalState(t *testing.T) {
	embedder := &testEmbedder{dimension: 3}
	tests := []struct {
		name    string
		options []ServiceOpt
		match   string
	}{
		{name: "missing base URL", options: []ServiceOpt{WithEmbedder(embedder)}, match: "base URL"},
		{name: "missing embedder", options: []ServiceOpt{WithBaseURL("http://localhost")}, match: "embedder"},
		{
			name: "invalid scheme",
			options: []ServiceOpt{
				WithBaseURL("ftp://localhost"), WithEmbedder(embedder),
			},
			match: "scheme",
		},
		{
			name: "missing host",
			options: []ServiceOpt{
				WithBaseURL("http:///prefix"), WithEmbedder(embedder),
			},
			match: "host",
		},
		{
			name: "URL user information",
			options: []ServiceOpt{
				WithBaseURL("https://user@example.com"), WithEmbedder(embedder),
			},
			match: "user information",
		},
		{
			name: "URL query",
			options: []ServiceOpt{
				WithBaseURL("https://example.com?secret=value"), WithEmbedder(embedder),
			},
			match: "query",
		},
		{
			name: "URL fragment",
			options: []ServiceOpt{
				WithBaseURL("https://example.com/#fragment"), WithEmbedder(embedder),
			},
			match: "fragment",
		},
		{
			name: "remote API key over HTTP",
			options: []ServiceOpt{
				WithBaseURL("http://example.com"), WithEmbedder(embedder), WithAPIKey("secret"),
			},
			match: "https is required",
		},
		{
			name: "authentication conflict",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithAPIKey("key"), WithBearerToken("token"),
			},
			match: "mutually exclusive",
		},
		{
			name: "custom authentication pair",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithHTTPHeaders(map[string]string{
					"Authorization":  "custom",
					"X-Chroma-Token": "custom",
				}),
				WithTenant("tenant"), WithDatabase("database"),
			},
			match: "mutually exclusive",
		},
		{
			name: "custom authentication conflict",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithAPIKey("key"),
				WithHTTPHeaders(map[string]string{"X-Chroma-Token": "custom"}),
				WithTenant("tenant"), WithDatabase("database"),
			},
			match: "conflicts",
		},
		{
			name: "custom authentication needs scope",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithHTTPHeaders(map[string]string{"Authorization": "custom"}),
			},
			match: "tenant and database",
		},
		{
			name: "canonical header duplicate",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithHTTPHeaders(map[string]string{"x-test": "one", "X-Test": "two"}),
			},
			match: "duplicate HTTP header",
		},
		{
			name: "header newline",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithHTTPHeaders(map[string]string{"X-Test": "one\r\ntwo"}),
			},
			match: "invalid value",
		},
		{
			name: "threshold",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithSimilarityThreshold(1.01),
			},
			match: "similarity threshold",
		},
		{
			name: "threshold NaN",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithSimilarityThreshold(math.NaN()),
			},
			match: "similarity threshold",
		},
		{
			name: "dimension mismatch",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithIndexDimension(4),
			},
			match: "embedding dimension mismatch",
		},
		{
			name: "timeout",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithTimeout(0),
			},
			match: "timeout",
		},
		{
			name: "job timeout",
			options: []ServiceOpt{
				WithBaseURL("http://localhost"), WithEmbedder(embedder),
				WithMemoryJobTimeout(-time.Second),
			},
			match: "memory job timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := defaultServiceOpts()
			for _, option := range tt.options {
				option(&opts)
			}
			err := normalizeAndValidateOptions(&opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestCollectionNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "minimum", value: "a_1"},
		{name: "maximum", value: "a" + stringOfLength('b', 510) + "1"},
		{name: "period", value: "a.b"},
		{name: "too short", value: "ab", wantErr: true},
		{name: "too long", value: "a" + stringOfLength('b', 511) + "1", wantErr: true},
		{name: "uppercase", value: "Memory", wantErr: true},
		{name: "leading hyphen", value: "-memory", wantErr: true},
		{name: "trailing underscore", value: "memory_", wantErr: true},
		{name: "consecutive periods", value: "memory..one", wantErr: true},
		{name: "IPv4 address", value: "127.0.0.1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCollectionName(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestBaseURLAllowsLoopbackCredentialsAndPathPrefix(t *testing.T) {
	opts := defaultServiceOpts()
	opts.baseURL = "http://127.0.0.1:8000/chroma/api/"
	opts.apiKey = "secret"
	opts.embedder = &testEmbedder{dimension: 3}

	require.NoError(t, normalizeAndValidateOptions(&opts))
	assert.Equal(t, "http://127.0.0.1:8000/chroma/api", opts.baseURL)
}

func TestBaseURLRequiresHTTPSForRemoteCustomHeaders(t *testing.T) {
	opts := defaultServiceOpts()
	opts.baseURL = "http://example.com"
	opts.headers = map[string]string{"X-Request-ID": "sensitive"}
	opts.embedder = &testEmbedder{dimension: 3}

	err := normalizeAndValidateOptions(&opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "https is required")
	assert.NotContains(t, err.Error(), "sensitive")
}

func stringOfLength(value byte, length int) string {
	result := make([]byte, length)
	for i := range result {
		result[i] = value
	}
	return string(result)
}
