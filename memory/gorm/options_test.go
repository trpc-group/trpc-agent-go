//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gormmemory

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions
	assert.Equal(t, defaultTableName, opts.tableName)
	assert.Equal(t, imemory.DefaultMemoryLimit, opts.memoryLimit)
	assert.Equal(t, imemory.DefaultSearchMinScore, opts.searchMinScore)
	assert.Equal(t, imemory.DefaultMaxSearchResults, opts.maxSearchResults)
	assert.False(t, opts.softDelete)
	assert.False(t, opts.skipDBInit)
}

func TestServiceOpts_clone(t *testing.T) {
	opts := defaultOptions.clone()
	opts.enabledTools[memory.AddToolName] = struct{}{}

	cloned := opts.clone()
	assert.Equal(t, opts.tableName, cloned.tableName)
	assert.Equal(t, opts.memoryLimit, cloned.memoryLimit)

	delete(cloned.enabledTools, memory.AddToolName)
	_, stillPresent := opts.enabledTools[memory.AddToolName]
	assert.True(t, stillPresent, "clone should copy enabledTools independently")
}

func TestServiceOpts_WithTableName(t *testing.T) {
	opts := ServiceOpts{}
	WithTableName("custom_memories")(&opts)
	assert.Equal(t, "custom_memories", opts.tableName)
}

func TestServiceOpts_WithTableName_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	opts := ServiceOpts{}
	WithTableName("invalid-table-name")(&opts)
}

func TestServiceOpts_WithSoftDelete(t *testing.T) {
	opts := ServiceOpts{}
	WithSoftDelete(true)(&opts)
	assert.True(t, opts.softDelete)
	WithSoftDelete(false)(&opts)
	assert.False(t, opts.softDelete)
}

func TestServiceOpts_WithMemoryLimit(t *testing.T) {
	opts := ServiceOpts{}
	WithMemoryLimit(42)(&opts)
	assert.Equal(t, 42, opts.memoryLimit)
}

func TestServiceOpts_WithSearchOptions(t *testing.T) {
	opts := ServiceOpts{}
	WithMinSearchScore(0.75)(&opts)
	WithMaxResults(5)(&opts)
	assert.Equal(t, 0.75, opts.searchMinScore)
	assert.Equal(t, 5, opts.maxSearchResults)
}

func TestServiceOpts_WithSearchOptions_NegativeIgnored(t *testing.T) {
	opts := defaultOptions.clone()
	WithMinSearchScore(-1)(&opts)
	WithMaxResults(-5)(&opts)
	assert.Equal(t, imemory.DefaultSearchMinScore, opts.searchMinScore)
	assert.Equal(t, imemory.DefaultMaxSearchResults, opts.maxSearchResults)
}

func TestServiceOpts_DeprecatedSearchOptions(t *testing.T) {
	opts := ServiceOpts{}
	WithSearchMinScore(0.5)(&opts)
	WithMaxSearchResults(3)(&opts)
	assert.Equal(t, 0.5, opts.searchMinScore)
	assert.Equal(t, 3, opts.maxSearchResults)
}

func TestServiceOpts_WithToolEnabled(t *testing.T) {
	opts := ServiceOpts{}
	WithToolEnabled(memory.AddToolName, true)(&opts)
	_, enabled := opts.enabledTools[memory.AddToolName]
	assert.True(t, enabled)
	_, explicit := opts.userExplicitlySet[memory.AddToolName]
	assert.True(t, explicit)

	WithToolEnabled(memory.AddToolName, false)(&opts)
	_, enabled = opts.enabledTools[memory.AddToolName]
	assert.False(t, enabled)
}

func TestServiceOpts_WithToolExposedAndHidden(t *testing.T) {
	opts := ServiceOpts{}

	WithToolExposed(memory.AddToolName, true)(&opts)
	_, exposed := opts.toolExposed[memory.AddToolName]
	assert.True(t, exposed)

	WithToolExposed(memory.AddToolName, false)(&opts)
	_, exposed = opts.toolExposed[memory.AddToolName]
	assert.False(t, exposed)
	_, hidden := opts.toolHidden[memory.AddToolName]
	assert.True(t, hidden)

	WithToolHidden(memory.SearchToolName, true)(&opts)
	_, hidden = opts.toolHidden[memory.SearchToolName]
	assert.True(t, hidden)

	WithToolHidden(memory.SearchToolName, false)(&opts)
	_, hidden = opts.toolHidden[memory.SearchToolName]
	assert.False(t, hidden)
}

func TestServiceOpts_WithAsyncWorkerSettings(t *testing.T) {
	opts := ServiceOpts{}
	timeout := 250 * time.Millisecond

	WithAsyncMemoryNum(3)(&opts)
	WithMemoryQueueSize(16)(&opts)
	WithMemoryJobTimeout(timeout)(&opts)

	assert.Equal(t, 3, opts.asyncMemoryNum)
	assert.Equal(t, 16, opts.memoryQueueSize)
	assert.Equal(t, timeout, opts.memoryJobTimeout)
}

func TestServiceOpts_WithExtractor(t *testing.T) {
	opts := ServiceOpts{}
	ext := &fakeExtractor{}
	WithExtractor(ext)(&opts)
	assert.Same(t, ext, opts.extractor)
}
