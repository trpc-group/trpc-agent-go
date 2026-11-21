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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestServiceOptionsApply(t *testing.T) {
	creator := func() tool.Tool { return nil }

	tests := []struct {
		name  string
		opt   ServiceOpt
		check func(*testing.T, *ServiceOpts)
	}{
		{
			name: "WithAddresses",
			opt:  WithAddresses([]string{"http://es:9200"}),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, []string{"http://es:9200"}, o.addresses)
			},
		},
		{
			name: "WithUsername",
			opt:  WithUsername("elastic"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, "elastic", o.username)
			},
		},
		{
			name: "WithPassword",
			opt:  WithPassword("secret"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, "secret", o.password)
			},
		},
		{
			name: "WithAPIKey",
			opt:  WithAPIKey("key"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, "key", o.apiKey)
			},
		},
		{
			name: "WithCertificateFingerprint",
			opt:  WithCertificateFingerprint("fp"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, "fp", o.certificateFingerprint)
			},
		},
		{
			name: "WithCompressRequestBody",
			opt:  WithCompressRequestBody(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.compressRequestBody)
			},
		},
		{
			name: "WithEnableMetrics",
			opt:  WithEnableMetrics(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.enableMetrics)
			},
		},
		{
			name: "WithEnableDebugLogger",
			opt:  WithEnableDebugLogger(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.enableDebugLogger)
			},
		},
		{
			name: "WithRetryOnStatus",
			opt:  WithRetryOnStatus([]int{502}),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, []int{502}, o.retryOnStatus)
			},
		},
		{
			name: "WithMaxRetries",
			opt:  WithMaxRetries(3),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 3, o.maxRetries)
			},
		},
		{
			name: "WithVersion",
			opt:  WithVersion(elasticsearch.ESVersionV7),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, elasticsearch.ESVersionV7, o.version)
			},
		},
		{
			name: "WithExtraOptions",
			opt:  WithExtraOptions("extra"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, []any{"extra"}, o.extraOptions)
			},
		},
		{
			name: "WithIndexName",
			opt:  WithIndexName("custom-index"),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, "custom-index", o.indexName)
			},
		},
		{
			name: "WithIndexDimension",
			opt:  WithIndexDimension(768),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 768, o.indexDimension)
			},
		},
		{
			name: "WithMaxResults",
			opt:  WithMaxResults(5),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 5, o.maxResults)
			},
		},
		{
			name: "WithHybridCandidateLimit",
			opt:  WithHybridCandidateLimit(20),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 20, o.hybridCandidateLimit)
			},
		},
		{
			name: "WithSoftDelete",
			opt:  WithSoftDelete(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.softDelete)
			},
		},
		{
			name: "WithSkipIndexInit",
			opt:  WithSkipIndexInit(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.skipIndexInit)
			},
		},
		{
			name: "WithEmbedder",
			opt:  WithEmbedder(newStubEmbedder(testDimension)),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.NotNil(t, o.embedder)
			},
		},
		{
			name: "WithCustomTool",
			opt:  WithCustomTool(memory.SearchToolName, creator),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Contains(t, o.toolCreators, memory.SearchToolName)
				assert.Contains(t, o.enabledTools, memory.SearchToolName)
				assert.Contains(t, o.userExplicitlySet, memory.SearchToolName)
			},
		},
		{
			name: "WithToolEnabled",
			opt:  WithToolEnabled(memory.LoadToolName, false),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.NotContains(t, o.enabledTools, memory.LoadToolName)
				assert.Contains(t, o.userExplicitlySet, memory.LoadToolName)
			},
		},
		{
			name: "WithToolExposed",
			opt:  WithToolExposed(memory.SearchToolName, true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Contains(t, o.toolExposed, memory.SearchToolName)
				assert.NotContains(t, o.toolHidden, memory.SearchToolName)
			},
		},
		{
			name: "WithAutoMemoryExposedTools",
			opt:  WithAutoMemoryExposedTools(memory.SearchToolName),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Contains(t, o.toolExposed, memory.SearchToolName)
			},
		},
		{
			name: "WithAsyncMemoryNum",
			opt:  WithAsyncMemoryNum(4),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 4, o.asyncMemoryNum)
			},
		},
		{
			name: "WithMemoryQueueSize",
			opt:  WithMemoryQueueSize(64),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, 64, o.memoryQueueSize)
			},
		},
		{
			name: "WithMemoryJobTimeout",
			opt:  WithMemoryJobTimeout(time.Minute),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.Equal(t, time.Minute, o.memoryJobTimeout)
			},
		},
		{
			name: "WithDisableAutoMemoryOnExternalContext",
			opt:  WithDisableAutoMemoryOnExternalContext(true),
			check: func(t *testing.T, o *ServiceOpts) {
				assert.True(t, o.disableAutoMemoryOnExternalContext)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &ServiceOpts{}
			tt.opt(o)
			tt.check(t, o)
		})
	}
}

func TestServiceOptionsFallbackToDefaults(t *testing.T) {
	o := &ServiceOpts{}

	WithAsyncMemoryNum(0)(o)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, o.asyncMemoryNum)

	WithMemoryQueueSize(0)(o)
	assert.Equal(t, imemory.DefaultMemoryQueueSize, o.memoryQueueSize)

	WithIndexDimension(0)(o)
	assert.Equal(t, 0, o.indexDimension)

	WithMaxResults(0)(o)
	assert.Equal(t, 0, o.maxResults)

	WithHybridCandidateLimit(0)(o)
	assert.Equal(t, 0, o.hybridCandidateLimit)

	WithSimilarityThreshold(1.5)(o)
	assert.Equal(t, 0.0, o.similarityThreshold)

	WithIndexName("")(o)
	assert.Equal(t, "", o.indexName)

	// The hide branch populates toolHidden and clears toolExposed.
	WithToolExposed(memory.SearchToolName, true)(o)
	WithToolExposed(memory.SearchToolName, false)(o)
	assert.Contains(t, o.toolHidden, memory.SearchToolName)
	assert.NotContains(t, o.toolExposed, memory.SearchToolName)
}

func TestServiceOptionsIgnoreInvalidToolNames(t *testing.T) {
	o := &ServiceOpts{}

	WithCustomTool("not-a-tool", nil)(o)
	assert.Empty(t, o.toolCreators)

	WithCustomTool("not-a-tool", func() tool.Tool { return nil })(o)
	assert.Empty(t, o.toolCreators)

	WithToolEnabled("not-a-tool", true)(o)
	assert.Empty(t, o.enabledTools)

	WithToolExposed("not-a-tool", true)(o)
	assert.Empty(t, o.toolExposed)
}

func TestServiceOptionsCloneIsolatesMaps(t *testing.T) {
	creator := func() tool.Tool { return nil }
	o := ServiceOpts{
		toolCreators: map[string]memory.ToolCreator{memory.SearchToolName: creator},
		enabledTools: map[string]struct{}{memory.SearchToolName: {}},
		toolExposed:  map[string]struct{}{memory.SearchToolName: {}},
		toolHidden:   map[string]struct{}{memory.SearchToolName: {}},
	}

	c := o.clone()
	c.toolCreators[memory.AddToolName] = func() tool.Tool { return nil }
	c.enabledTools[memory.AddToolName] = struct{}{}
	c.toolExposed[memory.AddToolName] = struct{}{}
	c.toolHidden[memory.AddToolName] = struct{}{}

	assert.NotContains(t, o.toolCreators, memory.AddToolName)
	assert.NotContains(t, o.enabledTools, memory.AddToolName)
	assert.NotContains(t, o.toolExposed, memory.AddToolName)
	assert.NotContains(t, o.toolHidden, memory.AddToolName)
	require.NotNil(t, c.userExplicitlySet)
}
