//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package database

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/database"
)

// ServiceOpts is the options for the database session service.
type ServiceOpts struct {
	sessionEventLimit  int
	dsn                string
	driverType         storage.DriverType // Database driver type (mysql, postgres, sqlite)
	instanceName       string
	extraOptions       []any
	sessionTTL         time.Duration // TTL for session state and event list
	appStateTTL        time.Duration // TTL for app state
	userStateTTL       time.Duration // TTL for user state
	autoCreateTable    bool          // Whether to auto create tables if not exist (default: true)
	autoMigrate        bool          // Whether to auto migrate existing tables (default: false)
	cleanupInterval    time.Duration // Interval for cleanup of expired data
	enableAsyncPersist bool
	asyncPersisterNum  int // number of worker goroutines for async persistence
	// summarizer integrates LLM summarization.
	summarizer summary.SessionSummarizer
	// asyncSummaryNum is the number of worker goroutines for async summary.
	asyncSummaryNum int
	// summaryQueueSize is the size of summary job queue.
	summaryQueueSize int
	// summaryJobTimeout is the timeout for processing a single summary job.
	summaryJobTimeout time.Duration
}

// ServiceOpt is the option for the database session service.
type ServiceOpt func(*ServiceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithDatabaseDSN creates a database client from DSN and sets it to the service.
// Supports MySQL, PostgreSQL, and other GORM-compatible databases.
// Use WithDriverType to specify the database type if not using MySQL.
func WithDatabaseDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithDriverType sets the database driver type.
// Supported types: storage.DriverMySQL (default), storage.DriverPostgreSQL, storage.DriverSQLite
func WithDriverType(driverType storage.DriverType) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.driverType = driverType
	}
}

// WithDatabaseInstance uses a database instance from storage.
// Note: WithDatabaseDSN has higher priority than WithDatabaseInstance.
// If both are specified, WithDatabaseDSN will be used.
func WithDatabaseInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the database session service.
// this option mainly used for the customized database client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// If not set, session will not expire automatically, set 0 will not expire.
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

// WithAutoCreateTable enables automatic table creation if tables don't exist.
// Default is true. Set to false to require manual table creation.
func WithAutoCreateTable(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.autoCreateTable = enable
	}
}

// WithAutoMigrate enables automatic table migration for existing tables.
// Default is false to prevent unintended schema changes in production.
// This includes adding missing columns and updating column definitions.
func WithAutoMigrate(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.autoMigrate = enable
	}
}

// WithCleanupInterval sets the interval for automatic cleanup of expired data.
// Default is 5 minutes if any TTL is configured.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.cleanupInterval = interval
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
