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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultSessionEventLimit     = 1000
	defaultChanBufferSize        = 100
	defaultAsyncPersisterNum     = 10
	defaultCleanupIntervalSecond = 5 * time.Minute // 5 min

	defaultAsyncPersistTimeout = 10 * time.Second

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100
)

// ServiceOpts is the options for the MySQL session service.
type ServiceOpts struct {
	sessionEventLimit int

	// MySQL connection settings (using DSN or instance name)
	dsn          string // MySQL DSN connection string (recommended)
	instanceName string // Pre-registered MySQL instance name
	extraOptions []any  // Extra options passed to storage layer

	sessionTTL         time.Duration // TTL for session state and event list
	appStateTTL        time.Duration // TTL for app state
	userStateTTL       time.Duration // TTL for user state
	enableAsyncPersist bool
	asyncPersisterNum  int           // number of worker goroutines for async persistence
	softDelete         bool          // enable soft delete (default: true)
	cleanupInterval    time.Duration // interval for automatic cleanup of expired data

	// summarizer integrates LLM summarization.
	summarizer summary.SessionSummarizer
	// asyncSummaryNum is the number of worker goroutines for async summary.
	asyncSummaryNum int
	// summaryQueueSize is the size of summary job queue.
	summaryQueueSize int
	// summaryJobTimeout is the timeout for processing a single summary job.
	summaryJobTimeout time.Duration

	// skipDBInit skips database initialization (table and index creation).
	// Useful when user doesn't have DDL permissions or when tables are managed externally.
	skipDBInit bool

	// tablePrefix is the prefix for all table names.
	// Default is empty string (no prefix).
	tablePrefix string

	// hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
}

// ServiceOpt is the option for the MySQL session service.
type ServiceOpt func(*ServiceOpts)

var (
	defaultOptions = ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		asyncPersisterNum: defaultAsyncPersisterNum,
		asyncSummaryNum:   defaultAsyncSummaryNum,
		summaryQueueSize:  defaultSummaryQueueSize,
		softDelete:        true, // default: enable soft delete
	}
)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithMySQLClientDSN sets the MySQL DSN connection string directly (recommended).
// Example: "user:password@tcp(localhost:3306)/sessions?parseTime=true&charset=utf8mb4"
//
// This is the preferred way to connect to MySQL as it:
// - Simplifies configuration (all connection params in one string)
// - Supports all MySQL connection parameters
// - Is consistent with memory/mysql and storage/mysql
func WithMySQLClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithMySQLInstance uses a MySQL instance from storage.
// The instance must be registered via storage.RegisterMySQLInstance() before use.
//
// Note: WithMySQLClientDSN has higher priority than WithMySQLInstance.
// If both are specified, DSN will be used.
func WithMySQLInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the MySQL session service.
// These options will be passed to the MySQL client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
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

// WithSoftDelete enables or disables soft delete.
// When enabled (default), DELETE operations set deleted_at timestamp instead of removing records.
// When disabled, DELETE operations permanently remove records from database.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enable
	}
}

// WithCleanupInterval sets the interval for automatic cleanup of expired data.
// If set to 0, automatic cleanup will be determined based on TTL configuration.
// Default cleanup interval is 5 minutes if any TTL is configured.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.cleanupInterval = interval
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
