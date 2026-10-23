//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions.clone()
	assert.Equal(t, defaultDatabase, opts.database)
	assert.Equal(t, defaultCollectionName, opts.collectionName)
	assert.Equal(t, imemory.DefaultMemoryLimit, opts.memoryLimit)
	assert.Equal(t, imemory.DefaultSearchMinScore, opts.searchMinScore)
	assert.Equal(t, imemory.DefaultMaxSearchResults, opts.maxSearchResults)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	assert.False(t, opts.softDelete)
	assert.False(t, opts.skipDBInit)
	assert.Nil(t, opts.extractor)
	assert.Equal(t, len(imemory.AllToolCreators), len(opts.toolCreators))
	for name := range imemory.AllToolCreators {
		assert.Contains(t, opts.toolCreators, name)
	}
	assert.Equal(t, imemory.DefaultEnabledTools, opts.enabledTools)
}

func TestCloneIsolation(t *testing.T) {
	opts := defaultOptions.clone()
	opts.toolCreators["custom"] = nil
	opts.enabledTools["custom"] = struct{}{}
	opts.toolExposed = map[string]struct{}{"x": {}}
	opts.toolHidden = map[string]struct{}{"y": {}}

	// Mutating a clone must not leak into the package-level defaults.
	assert.NotContains(t, defaultOptions.toolCreators, "custom")
	assert.NotContains(t, defaultOptions.enabledTools, "custom")
	assert.Nil(t, defaultOptions.toolExposed)
	assert.Nil(t, defaultOptions.toolHidden)
	// userExplicitlySet always starts empty on a fresh clone.
	assert.Empty(t, opts.userExplicitlySet)
}

func TestWithMongoClientURI(t *testing.T) {
	opts := defaultOptions.clone()
	WithMongoClientURI("mongodb://localhost:27017")(&opts)
	assert.Equal(t, "mongodb://localhost:27017", opts.uri)
}

func TestWithMongoInstance(t *testing.T) {
	opts := defaultOptions.clone()
	WithMongoInstance("my-instance")(&opts)
	assert.Equal(t, "my-instance", opts.instanceName)
}

func TestWithDatabase(t *testing.T) {
	opts := defaultOptions.clone()
	WithDatabase("custom-db")(&opts)
	assert.Equal(t, "custom-db", opts.database)
}

func TestWithCollectionName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithCollectionName("my_memories_2026")(&opts)
		assert.Equal(t, "my_memories_2026", opts.collectionName)
	})

	t.Run("invalid names panic", func(t *testing.T) {
		for _, name := range []string{
			"", "1starts-with-digit", "has-dash", "has space", "has.dot",
			"has$dollar", strings.Repeat("a", 65),
		} {
			assert.Panics(t, func() {
				WithCollectionName(name)(&ServiceOpts{})
			}, "name %q should panic", name)
		}
	})
}

func TestValidateCollectionName(t *testing.T) {
	assert.NoError(t, validateCollectionName("memories"))
	assert.NoError(t, validateCollectionName("_private"))
	assert.Error(t, validateCollectionName(""))
	assert.Error(t, validateCollectionName(strings.Repeat("a", 65)))
	assert.Error(t, validateCollectionName("bad-name"))
}

func TestWithSkipDBInit(t *testing.T) {
	opts := defaultOptions.clone()
	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)
}

func TestWithSoftDelete(t *testing.T) {
	opts := defaultOptions.clone()
	WithSoftDelete(true)(&opts)
	assert.True(t, opts.softDelete)
}

func TestWithMemoryLimit(t *testing.T) {
	opts := defaultOptions.clone()
	WithMemoryLimit(42)(&opts)
	assert.Equal(t, 42, opts.memoryLimit)
}

func TestWithMinSearchScore(t *testing.T) {
	opts := defaultOptions.clone()
	WithMinSearchScore(0.5)(&opts)
	assert.Equal(t, 0.5, opts.searchMinScore)

	// Negative values are ignored.
	WithMinSearchScore(-1)(&opts)
	assert.Equal(t, 0.5, opts.searchMinScore)
}

func TestWithMaxResults(t *testing.T) {
	opts := defaultOptions.clone()
	WithMaxResults(3)(&opts)
	assert.Equal(t, 3, opts.maxSearchResults)

	WithMaxResults(-1)(&opts)
	assert.Equal(t, 3, opts.maxSearchResults)
}

func TestWithCustomTool(t *testing.T) {
	t.Run("valid creator registers and enables the tool", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithCustomTool(memory.ClearToolName,
			func() tool.Tool { return memorytool.NewClearTool() })(&opts)
		assert.Contains(t, opts.toolCreators, memory.ClearToolName)
		assert.Contains(t, opts.enabledTools, memory.ClearToolName)
		assert.Contains(t, opts.userExplicitlySet, memory.ClearToolName)
	})

	t.Run("nil creator is ignored", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithCustomTool(memory.ClearToolName, nil)(&opts)
		assert.NotContains(t, opts.enabledTools, memory.ClearToolName,
			"nil creator must not enable the tool")
	})

	t.Run("invalid tool name ignored", func(t *testing.T) {
		opts := defaultOptions.clone()
		WithCustomTool("not_a_tool",
			func() tool.Tool { return memorytool.NewClearTool() })(&opts)
		assert.NotContains(t, opts.toolCreators, "not_a_tool")
	})
}

func TestWithToolEnabled(t *testing.T) {
	opts := defaultOptions.clone()
	WithToolEnabled(memory.DeleteToolName, true)(&opts)
	assert.Contains(t, opts.enabledTools, memory.DeleteToolName)
	assert.Contains(t, opts.userExplicitlySet, memory.DeleteToolName)

	WithToolEnabled(memory.AddToolName, false)(&opts)
	assert.NotContains(t, opts.enabledTools, memory.AddToolName)
	assert.Contains(t, opts.userExplicitlySet, memory.AddToolName)

	// Invalid names are ignored.
	WithToolEnabled("bogus", true)(&opts)
	assert.NotContains(t, opts.enabledTools, "bogus")
}

func TestWithToolExposed(t *testing.T) {
	opts := defaultOptions.clone()
	WithToolExposed(memory.AddToolName, true)(&opts)
	assert.Contains(t, opts.toolExposed, memory.AddToolName)

	WithToolExposed(memory.AddToolName, false)(&opts)
	assert.NotContains(t, opts.toolExposed, memory.AddToolName)
	assert.Contains(t, opts.toolHidden, memory.AddToolName)

	WithToolExposed("bogus", true)(&opts)
	assert.Empty(t, opts.toolExposed)
}

func TestWithAutoMemoryExposedTools(t *testing.T) {
	opts := defaultOptions.clone()
	WithAutoMemoryExposedTools(memory.AddToolName, "bogus")(&opts)
	assert.Contains(t, opts.toolExposed, memory.AddToolName)
	assert.NotContains(t, opts.toolExposed, "bogus")
}

func TestWithExtraOptions(t *testing.T) {
	opts := defaultOptions.clone()
	WithExtraOptions("a", 1)(&opts)
	WithExtraOptions("b")(&opts)
	assert.Equal(t, []any{"a", 1, "b"}, opts.extraOptions)
}

func TestWithExtractor(t *testing.T) {
	ext := &mockExtractor{}
	opts := defaultOptions.clone()
	WithExtractor(ext)(&opts)
	assert.Equal(t, ext, opts.extractor)
}

func TestWithAsyncMemoryNum(t *testing.T) {
	opts := defaultOptions.clone()
	WithAsyncMemoryNum(5)(&opts)
	assert.Equal(t, 5, opts.asyncMemoryNum)

	WithAsyncMemoryNum(0)(&opts)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
}

func TestWithMemoryQueueSize(t *testing.T) {
	opts := defaultOptions.clone()
	WithMemoryQueueSize(200)(&opts)
	assert.Equal(t, 200, opts.memoryQueueSize)

	WithMemoryQueueSize(0)(&opts)
	assert.Equal(t, imemory.DefaultMemoryQueueSize, opts.memoryQueueSize)
}

func TestWithMemoryJobTimeout(t *testing.T) {
	opts := defaultOptions.clone()
	WithMemoryJobTimeout(10 * time.Second)(&opts)
	assert.Equal(t, 10*time.Second, opts.memoryJobTimeout)
}

func TestWithDisableAutoMemoryOnExternalContext(t *testing.T) {
	opts := defaultOptions.clone()
	WithDisableAutoMemoryOnExternalContext(true)(&opts)
	assert.True(t, opts.disableAutoMemoryOnExternalContext)
}

func TestBuildClientOpts(t *testing.T) {
	t.Run("neither uri nor instance", func(t *testing.T) {
		_, err := buildClientOpts(ServiceOpts{})
		require.Error(t, err)
	})
}
