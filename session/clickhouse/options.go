//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clickhouse provides the ClickHouse session service.
package clickhouse

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit   = 1000
	defaultCleanupInterval     = 1 * time.Hour
	defaultAsyncPersisterNum   = 10
	defaultChanBufferSize      = 100
	defaultBatchSize           = 100
	defaultBatchTimeout        = 100 * time.Millisecond
	defaultAsyncSummaryNum     = 3
	defaultSummaryQueueSize    = 100
	defaultSummaryJobTimeout   = 60 * time.Second
	defaultAsyncPersistTimeout = 10 * time.Second
)

// ServiceOpts is the options for the ClickHouse session service.
type ServiceOpts struct {
	sessionEventLimit int

	// ClickHouse connection settings (using DSN or instance name)
	dsn          string // ClickHouse DSN connection string (recommended)
	instanceName string // Pre-registered ClickHouse instance name
	extraOptions []any  // Extra options passed to storage layer

	// Async persistence configuration
	enableAsyncPersist bool          // Enable application-level async persistence
	asyncPersisterNum  int           // Number of async worker goroutines
	batchSize          int           // Batch insert size
	batchTimeout       time.Duration // Batch flush timeout

	// TTL configuration
	sessionTTL   time.Duration // TTL for session state and event list
	appStateTTL  time.Duration // TTL for app state
	userStateTTL time.Duration // TTL for user state

	// Cleanup
	cleanupInterval time.Duration // Interval for automatic cleanup of expired/deleted data
	// deletedRetention configures the retention period for soft-deleted data.
	deletedRetention time.Duration // Retention period for soft-deleted data before physical cleanup

	// Summarizer integrates LLM summarization.
	summarizer summary.SessionSummarizer
	// asyncSummaryNum is the number of worker goroutines for async summary.
	asyncSummaryNum int
	// summaryQueueSize is the size of summary job queue.
	summaryQueueSize int
	// summaryJobTimeout is the timeout for processing a single summary job.
	summaryJobTimeout time.Duration

	// Schema management
	skipDBInit  bool   // Skip database initialization (table and index creation)
	tablePrefix string // Prefix for all table names

	// Hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
}

// ServiceOpt is the option for the ClickHouse session service.
type ServiceOpt func(*ServiceOpts)

var defaultOptions = ServiceOpts{
	sessionEventLimit: defaultSessionEventLimit,
	asyncPersisterNum: defaultAsyncPersisterNum,
	batchSize:         defaultBatchSize,
	batchTimeout:      defaultBatchTimeout,
	asyncSummaryNum:   defaultAsyncSummaryNum,
	summaryQueueSize:  defaultSummaryQueueSize,
	summaryJobTimeout: defaultSummaryJobTimeout,
	deletedRetention:  0, // Disabled by default, relying on Native TTL
}

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithClickHouseDSN sets the ClickHouse DSN connection string directly (recommended).
// Example: "clickhouse://user:password@localhost:9000/sessions?dial_timeout=10s"
//
// This is the preferred way to connect to ClickHouse as it:
// - Simplifies configuration (all connection params in one string)
// - Supports all ClickHouse connection parameters
// - Is consistent with storage/clickhouse
func WithClickHouseDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithClickHouseInstance uses a ClickHouse instance from storage.
// The instance must be registered via storage.RegisterClickHouseInstance() before use.
//
// Note: WithClickHouseDSN has higher priority than WithClickHouseInstance.
// If both are specified, DSN will be used.
func WithClickHouseInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the ClickHouse session service.
// These options will be passed to the ClickHouse client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithEnableAsyncPersist enables async persistence for session state and event list.
// Default is false.
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

// WithBatchSize sets the batch insert size for async persistence.
func WithBatchSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = defaultBatchSize
		}
		opts.batchSize = size
	}
}

// WithBatchTimeout sets the batch flush timeout for async persistence.
func WithBatchTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		if timeout <= 0 {
			timeout = defaultBatchTimeout
		}
		opts.batchTimeout = timeout
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// If not set or set to 0, sessions will not expire.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
// If not set or set to 0, app state will not expire.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
// If not set or set to 0, user state will not expire.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithCleanupInterval sets the interval for automatic cleanup of expired/deleted data.
// If set to 0, automatic cleanup will be determined based on TTL configuration.
// Deprecated: ClickHouse Native TTL is recommended over application-level cleanup.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.cleanupInterval = interval
	}
}

// WithDeletedRetention sets the retention period for soft-deleted data.
//
// Caution: This option relies on application-level periodic cleanup (using ALTER TABLE DELETE),
// which may have performance impact on large datasets.
// For production environments with large data volume, it is recommended to use
// ClickHouse Native TTL for physical cleanup instead of this option.
func WithDeletedRetention(retention time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.deletedRetention = retention
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

// WithSkipDBInit skips database initialization (table and index creation).
// Useful when:
// - User doesn't have DDL permissions
// - Tables are managed by migration tools
// - Running in production environment where schema is pre-created
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
// For example, with prefix "trpc", tables will be named:
// - trpc_session_states
// - trpc_session_events
// - etc.
//
// Note: An underscore will be automatically added if not present.
// "trpc" and "trpc_" both result in "trpc_" prefix.
//
// Security: Uses internal/session/sqldb.ValidateTablePrefix to prevent SQL injection.
func WithTablePrefix(prefix string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if prefix == "" {
			opts.tablePrefix = ""
			return
		}

		// Use the common validation logic from internal/session/sqldb
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
