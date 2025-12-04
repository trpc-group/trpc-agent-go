//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithSessionEventLimit(t *testing.T) {
	opts := &ServiceOpts{}
	WithSessionEventLimit(100)(opts)
	assert.Equal(t, 100, opts.sessionEventLimit)
}

func TestWithMySQLClientDSN(t *testing.T) {
	opts := &ServiceOpts{}
	dsn := "user:password@tcp(localhost:3306)/sessions"
	WithMySQLClientDSN(dsn)(opts)
	assert.Equal(t, dsn, opts.dsn)
}

func TestWithMySQLInstance(t *testing.T) {
	opts := &ServiceOpts{}
	instanceName := "default"
	WithMySQLInstance(instanceName)(opts)
	assert.Equal(t, instanceName, opts.instanceName)
}

func TestWithExtraOptions(t *testing.T) {
	opts := &ServiceOpts{}
	extra1, extra2 := "option1", "option2"
	WithExtraOptions(extra1, extra2)(opts)
	assert.Len(t, opts.extraOptions, 2)
	assert.Equal(t, extra1, opts.extraOptions[0])
	assert.Equal(t, extra2, opts.extraOptions[1])
}

func TestWithSessionTTL(t *testing.T) {
	opts := &ServiceOpts{}
	ttl := 24 * time.Hour
	WithSessionTTL(ttl)(opts)
	assert.Equal(t, ttl, opts.sessionTTL)
}

func TestWithAppStateTTL(t *testing.T) {
	opts := &ServiceOpts{}
	ttl := 48 * time.Hour
	WithAppStateTTL(ttl)(opts)
	assert.Equal(t, ttl, opts.appStateTTL)
}

func TestWithUserStateTTL(t *testing.T) {
	opts := &ServiceOpts{}
	ttl := 72 * time.Hour
	WithUserStateTTL(ttl)(opts)
	assert.Equal(t, ttl, opts.userStateTTL)
}

func TestWithEnableAsyncPersist(t *testing.T) {
	opts := &ServiceOpts{}
	WithEnableAsyncPersist(true)(opts)
	assert.True(t, opts.enableAsyncPersist)

	WithEnableAsyncPersist(false)(opts)
	assert.False(t, opts.enableAsyncPersist)
}

func TestWithAsyncPersisterNum(t *testing.T) {
	opts := &ServiceOpts{}

	// Valid number
	WithAsyncPersisterNum(5)(opts)
	assert.Equal(t, 5, opts.asyncPersisterNum)

	// Invalid number (< 1) should default
	WithAsyncPersisterNum(0)(opts)
	assert.Equal(t, defaultAsyncPersisterNum, opts.asyncPersisterNum)

	WithAsyncPersisterNum(-1)(opts)
	assert.Equal(t, defaultAsyncPersisterNum, opts.asyncPersisterNum)
}

func TestWithSummarizer(t *testing.T) {
	opts := &ServiceOpts{}
	summarizer := &mockSummarizer{}
	WithSummarizer(summarizer)(opts)
	assert.Equal(t, summarizer, opts.summarizer)
}

func TestWithAsyncSummaryNum(t *testing.T) {
	opts := &ServiceOpts{}

	// Valid number
	WithAsyncSummaryNum(3)(opts)
	assert.Equal(t, 3, opts.asyncSummaryNum)

	// Invalid number (< 1) should default
	WithAsyncSummaryNum(0)(opts)
	assert.Equal(t, defaultAsyncSummaryNum, opts.asyncSummaryNum)

	WithAsyncSummaryNum(-5)(opts)
	assert.Equal(t, defaultAsyncSummaryNum, opts.asyncSummaryNum)
}

func TestWithSummaryQueueSize(t *testing.T) {
	opts := &ServiceOpts{}

	// Valid size
	WithSummaryQueueSize(100)(opts)
	assert.Equal(t, 100, opts.summaryQueueSize)

	// Invalid size (< 1) should default
	WithSummaryQueueSize(0)(opts)
	assert.Equal(t, defaultSummaryQueueSize, opts.summaryQueueSize)

	WithSummaryQueueSize(-10)(opts)
	assert.Equal(t, defaultSummaryQueueSize, opts.summaryQueueSize)
}

func TestWithSummaryJobTimeout(t *testing.T) {
	opts := &ServiceOpts{}

	// Valid timeout
	timeout := 5 * time.Minute
	WithSummaryJobTimeout(timeout)(opts)
	assert.Equal(t, timeout, opts.summaryJobTimeout)

	// Invalid timeout (<= 0) should be ignored
	WithSummaryJobTimeout(0)(opts)
	assert.Equal(t, timeout, opts.summaryJobTimeout) // Should keep previous value

	WithSummaryJobTimeout(-1 * time.Second)(opts)
	assert.Equal(t, timeout, opts.summaryJobTimeout) // Should keep previous value
}

func TestWithSoftDelete(t *testing.T) {
	opts := &ServiceOpts{}

	WithSoftDelete(true)(opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(opts)
	assert.False(t, opts.softDelete)
}

func TestWithCleanupInterval(t *testing.T) {
	opts := &ServiceOpts{}
	interval := 10 * time.Minute
	WithCleanupInterval(interval)(opts)
	assert.Equal(t, interval, opts.cleanupInterval)

	// Zero interval is valid (disables auto cleanup)
	WithCleanupInterval(0)(opts)
	assert.Equal(t, time.Duration(0), opts.cleanupInterval)
}

func TestWithSkipDBInit(t *testing.T) {
	opts := &ServiceOpts{}

	WithSkipDBInit(true)(opts)
	assert.True(t, opts.skipDBInit)

	WithSkipDBInit(false)(opts)
	assert.False(t, opts.skipDBInit)
}

func TestWithTablePrefix(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected string
	}{
		{"Simple prefix", "trpc", "trpc"},
		{"Prefix with underscore", "trpc_", "trpc_"},
		{"Empty prefix", "", ""},
		{"Complex prefix", "my_app", "my_app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ServiceOpts{}
			WithTablePrefix(tt.prefix)(opts)
			assert.Equal(t, tt.expected, opts.tablePrefix)
		})
	}
}

func TestWithTablePrefix_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{"SQL injection attempt", "'; DROP TABLE users--"},
		{"Special characters", "prefix@#$%"},
		{"Spaces", "prefix with spaces"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ServiceOpts{}
			// Should panic due to validation
			require.Panics(t, func() {
				WithTablePrefix(tt.prefix)(opts)
			})
		})
	}
}

func TestMultipleOptions(t *testing.T) {
	opts := &ServiceOpts{}

	// Apply multiple options
	WithSessionEventLimit(200)(opts)
	WithSessionTTL(24 * time.Hour)(opts)
	WithAppStateTTL(48 * time.Hour)(opts)
	WithSoftDelete(true)(opts)
	WithAsyncPersisterNum(4)(opts)
	WithTablePrefix("trpc")(opts)

	// Verify all options are set
	assert.Equal(t, 200, opts.sessionEventLimit)
	assert.Equal(t, 24*time.Hour, opts.sessionTTL)
	assert.Equal(t, 48*time.Hour, opts.appStateTTL)
	assert.True(t, opts.softDelete)
	assert.Equal(t, 4, opts.asyncPersisterNum)
	assert.Equal(t, "trpc", opts.tablePrefix)
}

func TestDefaultOptions(t *testing.T) {
	// Test that default options are properly initialized
	opts := ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		asyncPersisterNum: defaultAsyncPersisterNum,
		softDelete:        true,
	}

	assert.Equal(t, defaultSessionEventLimit, opts.sessionEventLimit)
	assert.Equal(t, defaultAsyncPersisterNum, opts.asyncPersisterNum)
	assert.True(t, opts.softDelete)
}

func TestOptionsChaining(t *testing.T) {
	// Test that options can be chained and applied in order
	var serviceOpts []ServiceOpt

	serviceOpts = append(serviceOpts,
		WithSessionEventLimit(100),
		WithSessionTTL(1*time.Hour),
		WithSoftDelete(true),
	)

	opts := &ServiceOpts{}
	for _, opt := range serviceOpts {
		opt(opts)
	}

	assert.Equal(t, 100, opts.sessionEventLimit)
	assert.Equal(t, 1*time.Hour, opts.sessionTTL)
	assert.True(t, opts.softDelete)
}

func TestOptionsOverride(t *testing.T) {
	// Test that later options can override earlier ones
	opts := &ServiceOpts{}

	WithSessionEventLimit(100)(opts)
	assert.Equal(t, 100, opts.sessionEventLimit)

	// Override with new value
	WithSessionEventLimit(200)(opts)
	assert.Equal(t, 200, opts.sessionEventLimit)

	// Same for boolean options
	WithSoftDelete(true)(opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(opts)
	assert.False(t, opts.softDelete)
}
