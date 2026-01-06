//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestServiceOptions(t *testing.T) {
	tests := []struct {
		name     string
		opts     []ServiceOpt
		validate func(*testing.T, *ServiceOpts)
	}{
		{
			name: "WithSessionEventLimit",
			opts: []ServiceOpt{
				WithSessionEventLimit(500),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 500, opts.sessionEventLimit)
			},
		},
		{
			name: "WithClickHouseDSN",
			opts: []ServiceOpt{
				WithClickHouseDSN("clickhouse://localhost:9000"),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, "clickhouse://localhost:9000", opts.dsn)
			},
		},
		{
			name: "WithClickHouseInstance",
			opts: []ServiceOpt{
				WithClickHouseInstance("my-clickhouse"),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, "my-clickhouse", opts.instanceName)
			},
		},
		{
			name: "WithExtraOptions",
			opts: []ServiceOpt{
				WithExtraOptions("opt1", "opt2"),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, []any{"opt1", "opt2"}, opts.extraOptions)
			},
		},
		{
			name: "WithEnableAsyncPersist",
			opts: []ServiceOpt{
				WithEnableAsyncPersist(true),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.True(t, opts.enableAsyncPersist)
			},
		},
		{
			name: "WithAsyncPersisterNum",
			opts: []ServiceOpt{
				WithAsyncPersisterNum(20),
				WithAsyncPersisterNum(0), // Should use default
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, defaultAsyncPersisterNum, opts.asyncPersisterNum)
			},
		},
		{
			name: "WithBatchSize",
			opts: []ServiceOpt{
				WithBatchSize(500),
				WithBatchSize(0), // Should use default
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, defaultBatchSize, opts.batchSize)
			},
		},
		{
			name: "WithBatchTimeout",
			opts: []ServiceOpt{
				WithBatchTimeout(200 * time.Millisecond),
				WithBatchTimeout(0), // Should use default
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, defaultBatchTimeout, opts.batchTimeout)
			},
		},
		{
			name: "WithSessionTTL",
			opts: []ServiceOpt{
				WithSessionTTL(24 * time.Hour),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 24*time.Hour, opts.sessionTTL)
			},
		},
		{
			name: "WithAppStateTTL",
			opts: []ServiceOpt{
				WithAppStateTTL(12 * time.Hour),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 12*time.Hour, opts.appStateTTL)
			},
		},
		{
			name: "WithUserStateTTL",
			opts: []ServiceOpt{
				WithUserStateTTL(6 * time.Hour),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 6*time.Hour, opts.userStateTTL)
			},
		},
		{
			name: "WithCleanupInterval",
			opts: []ServiceOpt{
				WithCleanupInterval(10 * time.Minute),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 10*time.Minute, opts.cleanupInterval)
			},
		},
		{
			name: "WithDeletedRetention",
			opts: []ServiceOpt{
				WithDeletedRetention(48 * time.Hour),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 48*time.Hour, opts.deletedRetention)
			},
		},
		{
			name: "WithSummarizer",
			opts: []ServiceOpt{
				WithSummarizer(&mockSummarizer{}),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.NotNil(t, opts.summarizer)
			},
		},
		{
			name: "WithAsyncSummaryNum",
			opts: []ServiceOpt{
				WithAsyncSummaryNum(5),
				WithAsyncSummaryNum(0), // Should use default
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, defaultAsyncSummaryNum, opts.asyncSummaryNum)
			},
		},
		{
			name: "WithSummaryQueueSize",
			opts: []ServiceOpt{
				WithSummaryQueueSize(200),
				WithSummaryQueueSize(0), // Should use default
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, defaultSummaryQueueSize, opts.summaryQueueSize)
			},
		},
		{
			name: "WithSummaryJobTimeout",
			opts: []ServiceOpt{
				WithSummaryJobTimeout(5 * time.Minute),
				WithSummaryJobTimeout(0), // Should be ignored
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, 5*time.Minute, opts.summaryJobTimeout)
			},
		},
		{
			name: "WithSkipDBInit",
			opts: []ServiceOpt{
				WithSkipDBInit(true),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.True(t, opts.skipDBInit)
			},
		},
		{
			name: "WithTablePrefix",
			opts: []ServiceOpt{
				WithTablePrefix("test_prefix"),
				WithTablePrefix(""), // Should reset
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Equal(t, "", opts.tablePrefix)
			},
		},
		{
			name: "WithHooks",
			opts: []ServiceOpt{
				WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error { return nil }),
				WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
					return next()
				}),
			},
			validate: func(t *testing.T, opts *ServiceOpts) {
				assert.Len(t, opts.appendEventHooks, 1)
				assert.Len(t, opts.getSessionHooks, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ServiceOpts{}
			for _, opt := range tt.opts {
				opt(opts)
			}
			tt.validate(t, opts)
		})
	}
}

func TestWithTablePrefix_Validation(t *testing.T) {
	// Valid prefix
	opts := &ServiceOpts{}
	WithTablePrefix("valid_prefix")(opts)
	assert.Equal(t, "valid_prefix", opts.tablePrefix)

	// Invalid prefix should panic
	assert.Panics(t, func() {
		WithTablePrefix("invalid-prefix")(opts)
	})
}
