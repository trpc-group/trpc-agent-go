//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit = 1000

	defaultChanBufferSize = 100

	defaultAsyncPersisterNum = 1

	defaultCleanupInterval = 5 * time.Minute

	defaultDBInitTimeout = 30 * time.Second

	defaultAsyncPersistTimeout = 10 * time.Second

	defaultAsyncSummaryNum   = 3
	defaultSummaryQueueSize  = 100
	defaultSummaryJobTimeout = 60 * time.Second
)

// ServiceOpts is the options for the sqlite session service.
type ServiceOpts struct {
	sessionEventLimit int

	sessionTTL         time.Duration
	appStateTTL        time.Duration
	userStateTTL       time.Duration
	enableAsyncPersist bool
	asyncPersisterNum  int
	softDelete         bool
	cleanupInterval    time.Duration

	// summarizer integrates LLM summarization.
	summarizer         summary.SessionSummarizer
	summarizerResolver summary.SessionSummarizerResolver
	asyncSummaryNum    int
	summaryQueueSize   int
	summaryJobTimeout  time.Duration

	// skipDBInit skips database initialization.
	skipDBInit bool

	// tablePrefix is the prefix for all table names.
	tablePrefix string

	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
}

// ServiceOpt is the option for the sqlite session service.
type ServiceOpt func(*ServiceOpts)

var defaultOptions = ServiceOpts{
	sessionEventLimit: defaultSessionEventLimit,
	asyncPersisterNum: defaultAsyncPersisterNum,
	asyncSummaryNum:   defaultAsyncSummaryNum,
	summaryQueueSize:  defaultSummaryQueueSize,
	summaryJobTimeout: defaultSummaryJobTimeout,
	softDelete:        true,
}

// WithSessionEventLimit sets the event limit per session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state and event list.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithEnableAsyncPersist enables async persistence.
func WithEnableAsyncPersist(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of async persister workers.
func WithAsyncPersisterNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		opts.asyncPersisterNum = num
	}
}

// WithSoftDelete enables or disables soft delete.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enable
	}
}

// WithCleanupInterval sets the cleanup interval for expired data.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.cleanupInterval = interval
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
func WithSummarizer(s summary.SessionSummarizer) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summarizer = s
	}
}

// WithSessionSummarizerResolver injects a request-scoped summarizer resolver.
func WithSessionSummarizerResolver(p summary.SessionSummarizerResolver) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summarizerResolver = p
	}
}

// WithAsyncSummaryNum sets the number of async summary workers.
func WithAsyncSummaryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncSummaryNum
		}
		opts.asyncSummaryNum = num
	}
}

// WithSummaryQueueSize sets the size of the summary job queue.
func WithSummaryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		opts.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing one summary job.
func WithSummaryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithSkipDBInit skips database initialization (DDL).
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
//
// Security: Uses internal/session/sqldb.ValidateTablePrefix to prevent SQL
// injection.
func WithTablePrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if prefix == "" {
			opts.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		opts.tablePrefix = prefix
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(hooks ...session.AppendEventHook) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appendEventHooks = append(opts.appendEventHooks, hooks...)
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(hooks ...session.GetSessionHook) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}
