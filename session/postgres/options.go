//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"strings"
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
	defaultAsyncPersistTimeout   = 5 * time.Second

	defaultAsyncSummaryNum  = 3
	defaultSummaryQueueSize = 100

	defaultHost     = "localhost"
	defaultPort     = 5432
	defaultDatabase = "trpc-agent-go-pgsession"
	defaultSSLMode  = "disable"
)

// ServiceOpts is the options for the postgres session service.
type ServiceOpts struct {
	sessionEventLimit int

	// PostgreSQL connection settings
	dsn      string
	host     string
	port     int
	user     string
	password string
	database string
	sslMode  string

	instanceName string
	extraOptions []any

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
	// schema is the PostgreSQL schema name where tables are created.
	// Default is empty string (uses default schema, typically "public").
	schema string
	// hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
}

// ServiceOpt is the option for the postgres session service.
type ServiceOpt func(*ServiceOpts)

// WithPostgresClientDSN sets the PostgreSQL DSN connection string directly (recommended).
// Example: "postgres://user:password@localhost:5432/dbname?sslmode=disable"
//
// Note: WithPostgresClientDSN has the highest priority.
// If DSN is specified, other connection settings (WithHost, WithPort, etc.) will be ignored.
func WithPostgresClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

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
		summaryJobTimeout:  30 * time.Second,
		softDelete:         true, // Enable soft delete by default
		cleanupInterval:    0,
	}
)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithHost sets the PostgreSQL host.
func WithHost(host string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.host = host
	}
}

// WithPort sets the PostgreSQL port.
func WithPort(port int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.port = port
	}
}

// WithUser sets the username for authentication.
func WithUser(user string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.user = user
	}
}

// WithPassword sets the password for authentication.
func WithPassword(password string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.password = password
	}
}

// WithDatabase sets the database name.
func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.database = database
	}
}

// WithSSLMode sets the SSL mode for connection.
func WithSSLMode(sslMode string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sslMode = sslMode
	}
}

// WithPostgresInstance uses a postgres instance from storage.
// Note: Direct connection settings (WithHost, WithPort, etc.) have higher priority than WithPostgresInstance.
// If both are specified, direct connection settings will be used.
func WithPostgresInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the postgres session service.
// this option mainly used for the customized postgres client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// If not set, session will not expire, set 0 will not expire.
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

		// Use internal/session/sqldb validation
		sqldb.MustValidateTablePrefix(prefix)

		// Automatically add underscore if not present
		if !strings.HasSuffix(prefix, "_") {
			prefix += "_"
		}
		opts.tablePrefix = prefix
	}
}

// WithSchema sets the PostgreSQL schema name where tables will be created.
// If not set, tables will be created in the default schema (typically "public").
// For example, with schema "my_schema", tables will be qualified as:
// - my_schema.session_states
// - my_schema.session_events
// - etc.
//
// Note: The schema must already exist in the database before using this option.
// Security: Uses internal/session/sqldb.ValidateTableName to prevent SQL injection.
func WithSchema(schema string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if schema != "" {
			// Use internal/session/sqldb validation
			sqldb.MustValidateTableName(schema)
		}
		opts.schema = schema
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
