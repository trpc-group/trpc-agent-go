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

// CompatMode defines the V1/V2 compatibility mode for the redis session service.
type CompatMode int

const (
	// CompatModeNone disables V1 compatibility entirely.
	// - Read: V2 only
	// - Write: V2 only
	// Use this when all instances are upgraded and V1 data has expired.
	CompatModeNone CompatMode = iota

	// CompatModeLegacy enables V1 read fallback only (no dual-write).
	// - Read: V2 first, fallback to V1 if not found
	// - Write: V2 only
	// Use this after all instances are upgraded but V1 data still exists.
	CompatModeLegacy

	// CompatModeDualWrite enables full V1 compatibility with dual-write.
	// - Read: V2 first, fallback to V1 if not found
	// - Write: dual-write to both V2 and V1
	// Use this during rolling upgrades when old V1-only instances are still running.
	// This ensures old instances can read data created by new instances.
	CompatModeDualWrite
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
	// hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
	// compatMode controls V1/V2 compatibility behavior.
	// See CompatMode constants for details.
	// Default: CompatModeLegacy (safe for most scenarios).
	compatMode CompatMode
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
// If not set, session will expire in 30 min, set 0 will not expire.
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

// WithCompatMode sets the V1/V2 compatibility mode.
//
// Available modes:
//   - CompatModeNone: V2 only, no V1 compatibility
//   - CompatModeLegacy: V2 first with V1 read fallback (default)
//   - CompatModeDualWrite: Full compatibility with dual-write
//
// Migration path:
//  1. Rolling upgrade: WithCompatMode(CompatModeDualWrite) - old V1 instances can read new data
//  2. All upgraded: WithCompatMode(CompatModeLegacy) - stop dual-write, keep V1 read fallback
//  3. V1 TTL expired: WithCompatMode(CompatModeNone) - pure V2 mode
//
// Default: CompatModeLegacy (safe for most scenarios where V1 data may still exist).
func WithCompatMode(mode CompatMode) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.compatMode = mode
	}
}

// WithKeyPrefix sets the key prefix for all Redis keys.
// Both V1 and V2 keys will use this prefix:
//   - V1: prefix:sess:{app}:user, prefix:event:{app}:user:sess, etc.
//   - V2: prefix:v2:meta:{app:user}:sess, prefix:v2:evtdata:{app:user}:sess, etc.
//
// This is typically used to namespace keys when multiple applications share the same Redis instance.
func WithKeyPrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.keyPrefix = prefix
	}
}

// WithLegacySupport is deprecated. Use WithCompatMode instead.
//
// Deprecated: Use WithCompatMode(CompatModeLegacy) or WithCompatMode(CompatModeNone).
func WithLegacySupport(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if enable {
			opts.compatMode = CompatModeLegacy
		} else {
			opts.compatMode = CompatModeNone
		}
	}
}

// WithDualWrite is deprecated. Use WithCompatMode instead.
//
// Deprecated: Use WithCompatMode(CompatModeDualWrite).
func WithDualWrite(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if enable {
			opts.compatMode = CompatModeDualWrite
		}
	}
}
