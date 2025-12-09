//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit     = 1000
	defaultCleanupIntervalSecond = 5 * time.Minute // 5 min

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100
)

// serviceOpts is the options for session service.
type serviceOpts struct {
	// sessionEventLimit is the limit of events in a session.
	sessionEventLimit int
	// sessionTTL is the TTL for session state and event list.
	sessionTTL time.Duration
	// appStateTTL is the TTL for app state.
	appStateTTL time.Duration
	// userStateTTL is the TTL for user state.
	userStateTTL time.Duration
	// cleanupInterval is the interval for automatic cleanup of expired data.
	// If set to 0, automatic cleanup is disabled.
	cleanupInterval time.Duration
	// summarizer integrates LLM summarization.
	summarizer summary.SessionSummarizer
	// asyncSummaryNum is the number of worker goroutines for async summary.
	asyncSummaryNum int
	// summaryQueueSize is the size of summary job queue.
	summaryQueueSize int
	// summaryJobTimeout is the timeout for processing a single summary job.
	summaryJobTimeout time.Duration
	// appendEventHooks are hooks for AppendEvent.
	appendEventHooks []session.AppendEventHook
	// getSessionHooks are hooks for GetSession.
	getSessionHooks []session.GetSessionHook
}

// ServiceOpt is the option for the in-memory session service.
type ServiceOpt func(*serviceOpts)

var (
	defaultOptions = serviceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		cleanupInterval:   0,
		asyncSummaryNum:   defaultAsyncSummaryNum,
		summaryQueueSize:  defaultSummaryQueueSize,
		summaryJobTimeout: 30 * time.Second,
	}
)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// if not set, session will expire in 30 min, set 0 will not expire.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
// If not set, app state will not expire automatically.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
// If not set, user state will not expire automatically.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithCleanupInterval sets the interval for automatic cleanup of expired data.
// If set to 0, automatic cleanup will be determined based on TTL configuration.
// Default cleanup interval is 5 minutes if any TTL is configured.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.cleanupInterval = interval
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
func WithSummarizer(s summary.SessionSummarizer) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.summarizer = s
	}
}

// WithAsyncSummaryNum sets the number of workers for async summary processing.
func WithAsyncSummaryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = defaultAsyncSummaryNum
		}
		opts.asyncSummaryNum = num
	}
}

// WithSummaryQueueSize sets the size of the summary job queue.
func WithSummaryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		opts.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing a single summary job.
// If not set, a sensible default will be applied.
func WithSummaryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(hooks ...session.AppendEventHook) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.appendEventHooks = append(opts.appendEventHooks, hooks...)
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(hooks ...session.GetSessionHook) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}
