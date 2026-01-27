//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redisv2

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultEvictionBatchSize   = 10
	defaultAsyncPersistTimeout = 2 * time.Second
	defaultChanBufferSize      = 100
	defaultAsyncPersisterNum   = 10
	defaultAsyncSummaryNum     = 3
	defaultSummaryQueueSize    = 100
	defaultSummaryJobTimeout   = 60 * time.Second
)

// serviceOpts holds configuration for Service.
type serviceOpts struct {
	url                 string
	instanceName        string
	extraOptions        []any
	sessionTTL          time.Duration
	appStateTTL         time.Duration
	userStateTTL        time.Duration
	maxEventsPerSession int // 0 means no limit
	evictionBatchSize   int // default 10

	// async persist options
	enableAsyncPersist bool
	asyncPersisterNum  int

	// summarizer options
	summarizer        summary.SessionSummarizer
	asyncSummaryNum   int
	summaryQueueSize  int
	summaryJobTimeout time.Duration

	// hooks
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook

	// indexes
	indexes []eventIndex
}

// Option configures the Service.
type Option func(*serviceOpts)

var defaultOptions = serviceOpts{
	sessionTTL:          0,
	appStateTTL:         0,
	userStateTTL:        0,
	maxEventsPerSession: 0,
	evictionBatchSize:   defaultEvictionBatchSize,
	enableAsyncPersist:  false,
	asyncPersisterNum:   defaultAsyncPersisterNum,
	asyncSummaryNum:     defaultAsyncSummaryNum,
	summaryQueueSize:    defaultSummaryQueueSize,
	summaryJobTimeout:   defaultSummaryJobTimeout,
	indexes:             []eventIndex{&requestIDIndex{}},
}

// WithRedisClientURL sets the Redis URL.
func WithRedisClientURL(url string) Option {
	return func(opts *serviceOpts) {
		opts.url = url
	}
}

// WithRedisInstance sets the Redis instance name.
func WithRedisInstance(instanceName string) Option {
	return func(opts *serviceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets extra options for redis client builder.
func WithExtraOptions(extraOptions ...any) Option {
	return func(opts *serviceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session data.
func WithSessionTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
func WithAppStateTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
func WithUserStateTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithMaxEventsPerSession sets the maximum events per session.
// When exceeded, oldest events are automatically evicted.
// 0 means no limit.
func WithMaxEventsPerSession(max int) Option {
	return func(opts *serviceOpts) {
		opts.maxEventsPerSession = max
	}
}

// WithEvictionBatchSize sets how many events to evict at once.
func WithEvictionBatchSize(size int) Option {
	return func(opts *serviceOpts) {
		if size > 0 {
			opts.evictionBatchSize = size
		}
	}
}

// WithEnableAsyncPersist enables async persistence for events.
func WithEnableAsyncPersist(enable bool) Option {
	return func(opts *serviceOpts) {
		opts.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of workers for async persistence.
func WithAsyncPersisterNum(num int) Option {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		opts.asyncPersisterNum = num
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
func WithSummarizer(s summary.SessionSummarizer) Option {
	return func(opts *serviceOpts) {
		opts.summarizer = s
	}
}

// WithAsyncSummaryNum sets the number of workers for async summary processing.
func WithAsyncSummaryNum(num int) Option {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = defaultAsyncSummaryNum
		}
		opts.asyncSummaryNum = num
	}
}

// WithSummaryQueueSize sets the size of the summary job queue.
func WithSummaryQueueSize(size int) Option {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		opts.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing a single summary job.
func WithSummaryJobTimeout(timeout time.Duration) Option {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(hooks ...session.AppendEventHook) Option {
	return func(opts *serviceOpts) {
		opts.appendEventHooks = append(opts.appendEventHooks, hooks...)
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(hooks ...session.GetSessionHook) Option {
	return func(opts *serviceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}

// WithEventIndexes sets custom event indexes.
// By default, requestIDIndex is enabled. Use this to override or add more indexes.
func WithEventIndexes(indexes ...eventIndex) Option {
	return func(opts *serviceOpts) {
		opts.indexes = indexes
	}
}

// WithRequestIDIndex enables the default requestID index.
// This is enabled by default, use this only if you've cleared indexes with WithEventIndexes.
func WithRequestIDIndex() Option {
	return func(opts *serviceOpts) {
		opts.indexes = append(opts.indexes, &requestIDIndex{})
	}
}

// WithBranchIndex enables indexing by branch field.
func WithBranchIndex() Option {
	return func(opts *serviceOpts) {
		opts.indexes = append(opts.indexes, &branchIndex{})
	}
}
