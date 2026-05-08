//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit   = 1000
	defaultAsyncPersistTimeout = 2 * time.Second
	defaultChanBufferSize      = 100
	defaultAsyncPersisterNum   = 10

	defaultAsyncSummaryNum   = 3
	defaultSummaryQueueSize  = 100
	defaultSummaryJobTimeout = 60 * time.Second
)

// CompatMode defines the zset/hashidx compatibility mode for the redis session service.
// HashIdx is the improved version with separated data and index storage, offering:
//   - Better scalability: no hot spot on app-level hash tags
//   - Flexible indexing: supports custom indexes beyond time-based
//   - Cleaner data structure: Hash for data, ZSet for indexes
type CompatMode int

const (
	// CompatModeNone disables zset compatibility entirely.
	// - Read: hashidx only
	// - Write: hashidx only
	// Use this when all instances are upgraded and zset data has expired.
	CompatModeNone CompatMode = iota

	// CompatModeLegacy enables zset read fallback only (no dual-write).
	// - Read: zset first if data exists, fallback to hashidx
	// - Write: hashidx only
	// Use this after all instances are upgraded but zset data still exists.
	CompatModeLegacy

	// CompatModeTransition forces new session creation to use zset storage only.
	// - Read: zset first if data exists, fallback to hashidx
	// - Write: zset only (new sessions created in zset)
	// Use this during rolling upgrades when old zset-only instances are still running.
	// After all instances are upgraded, switch to CompatModeLegacy to start using hashidx.
	CompatModeTransition
)

// ServiceOpts is the options for the redis session service.
type ServiceOpts struct {
	sessionEventLimit  int
	url                string
	instanceName       string
	extraOptions       []any
	sessionTTL         time.Duration // TTL for session state and event list
	appStateTTL        time.Duration // TTL for app state
	userStateTTL       time.Duration // TTL for user state
	enableAsyncPersist bool
	asyncPersisterNum  int // number of worker goroutines for async persistence
	// keyPrefix is the prefix for all redis keys.
	// If set, all keys will be prefixed with this value followed by a colon.
	// For example, if keyPrefix is "myapp", key "sess:{app}:user" becomes "myapp:sess:{app}:user".
	keyPrefix string
	// summarizer integrates LLM summarization.
	summarizer summary.SessionSummarizer
	// asyncSummaryNum is the number of worker goroutines for async summary.
	asyncSummaryNum int
	// summaryQueueSize is the size of summary job queue.
	summaryQueueSize int
	// summaryJobTimeout is the timeout for processing a single summary job.
	summaryJobTimeout time.Duration
	// summaryFilterAllowlist restricts which non-empty filterKeys may trigger
	// branch summaries.
	summaryFilterAllowlist []string
	// cascadeFullSessionSummary controls whether allowed branch summaries also
	// refresh the full-session summary. Nil preserves the legacy default of
	// enabling full-session cascade for zero-value options.
	cascadeFullSessionSummary *bool
	// hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
	// compatMode controls zset/hashidx compatibility behavior.
	// See CompatMode constants for details.
	// Default: CompatModeLegacy (safe for most scenarios).
	compatMode             CompatMode
	enableTracing          bool
	enableUserSessionIndex bool
}

// ServiceOpt is the option for the redis session service.
type ServiceOpt func(*ServiceOpts)

var (
	defaultOptions = ServiceOpts{
		sessionEventLimit:  defaultSessionEventLimit,
		sessionTTL:         0,
		appStateTTL:        0,
		userStateTTL:       0,
		asyncPersisterNum:  defaultAsyncPersisterNum,
		enableAsyncPersist: false,
		asyncSummaryNum:    defaultAsyncSummaryNum,
		summaryQueueSize:   defaultSummaryQueueSize,
		summaryJobTimeout:  defaultSummaryJobTimeout,
		compatMode:         CompatModeLegacy,
	}
)

func (opts ServiceOpts) shouldCascadeFullSessionSummary() bool {
	if opts.cascadeFullSessionSummary == nil {
		return true
	}
	return *opts.cascadeFullSessionSummary
}

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.url = url
	}
}

// WithRedisInstance uses a redis instance from storage.
// Note: WithRedisClientURL has higher priority than WithRedisInstance.
// If both are specified, WithRedisClientURL will be used.
func WithRedisInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the redis session service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// Default is 0 (no expiration). TTL is refreshed on write operations
// (CreateSession, AppendEvent) but not on reads (GetSession).
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
// If not set, app state will not expire.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
// If not set, user state will not expire.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithEnableAsyncPersist enables async persistence for session state and event list.
// if not set, default is false.
func WithEnableAsyncPersist(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of workers for async persistence.
func WithAsyncPersisterNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		opts.asyncPersisterNum = num
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
func WithSummarizer(s summary.SessionSummarizer) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summarizer = s
	}
}

// WithAsyncSummaryNum sets the number of workers for async summary processing.
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

// WithSummaryJobTimeout sets the timeout for processing a single summary job.
// If not set, a sensible default will be applied.
func WithSummaryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithSummaryFilterAllowlist restricts which non-empty filterKeys may trigger
// branch summaries. Keys use the same exact format as event filter keys.
func WithSummaryFilterAllowlist(filterKeys ...string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summaryFilterAllowlist = append([]string{}, filterKeys...)
	}
}

// WithCascadeFullSessionSummary controls whether an allowed branch summary also
// refreshes the full-session summary keyed by SummaryFilterKeyAllContents.
func WithCascadeFullSessionSummary(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		enabled := enable
		opts.cascadeFullSessionSummary = &enabled
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

// WithCompatMode sets the zset/hashidx compatibility mode.
//
// Available modes:
//   - CompatModeNone: hashidx only, no zset compatibility
//   - CompatModeLegacy: hashidx write + zset read fallback (default)
//   - CompatModeTransition: zset only (identical behavior to old zset-only instances)
//
// Migration path:
//  1. Rolling upgrade: WithCompatMode(CompatModeTransition) - all nodes write zset, safe mixed deployment
//  2. All upgraded: WithCompatMode(CompatModeLegacy) - new sessions use hashidx, old sessions fallback to zset
//  3. zset TTL expired: WithCompatMode(CompatModeNone) - pure hashidx mode
//
// Default: CompatModeLegacy (safe for most scenarios where zset data may still exist).
func WithCompatMode(mode CompatMode) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.compatMode = mode
	}
}

// WithKeyPrefix sets the key prefix for all Redis keys.
// Both zset and hashidx keys will use this prefix:
//   - zset: prefix:sess:{app}:user, prefix:event:{app}:user:sess, etc.
//   - hashidx: prefix:hashidx:meta:app:{user}:sess, prefix:hashidx:evtdata:app:{user}:sess, etc.
//
// This is typically used to namespace keys when multiple applications share the same Redis instance.
func WithKeyPrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.keyPrefix = prefix
	}
}

// WithEnableTracing enables OpenTelemetry tracing for redis session operations.
func WithEnableTracing(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableTracing = enable
	}
}

// WithEnableUserSessionIndex enables the per-user session index Hash for HashIdx storage.
// When enabled:
//   - CreateSession atomically writes both meta key and index entry
//   - DeleteSession removes the index entry
//   - ListSessions uses HSCAN on the index instead of global SCAN
//
// When disabled (default):
//   - No index is written or maintained
//   - ListSessions falls back to SCAN on session meta keys
//   - No additional Redis overhead
func WithEnableUserSessionIndex(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.enableUserSessionIndex = enable
	}
}
