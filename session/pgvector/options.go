//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pgvector provides a PostgreSQL session service
// with built-in pgvector-based semantic search.
package pgvector

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Default settings.
const (
	defaultSessionEventLimit     = 1000
	defaultChanBufferSize        = 100
	defaultAsyncPersisterNum     = 10
	defaultCleanupIntervalSecond = 5 * time.Minute
	defaultEmbedTimeout          = 30 * time.Second

	defaultAsyncSummaryNum   = 3
	defaultSummaryQueueSize  = 100
	defaultSummaryJobTimeout = 60 * time.Second

	defaultHost     = "localhost"
	defaultPort     = 5432
	defaultDatabase = "trpc-agent-go-pgsession"
	defaultSSLMode  = "disable"

	defaultIndexDimension = 1536
	defaultMaxResults     = 5
	defaultHNSWM          = 16
	defaultHNSWEf         = 200
	defaultHybridRRFK     = 60
	defaultCandidateRatio = 3
)

// IndexTextBuilder customizes the searchable text stored
// for an event before embedding.
type IndexTextBuilder func(
	sess *session.Session,
	evt *event.Event,
	baseText string,
	role model.Role,
) string

// ServiceOpts holds all configuration for the pgvector
// session service.
type ServiceOpts struct {
	sessionEventLimit int

	// PostgreSQL connection settings.
	dsn      string
	host     string
	port     int
	user     string
	password string
	database string
	sslMode  string

	instanceName string
	extraOptions []any

	sessionTTL         time.Duration
	appStateTTL        time.Duration
	userStateTTL       time.Duration
	enableAsyncPersist bool
	asyncPersisterNum  int
	softDelete         bool
	cleanupInterval    time.Duration

	// Summarizer integrates LLM summarization.
	summarizer        summary.SessionSummarizer
	asyncSummaryNum   int
	summaryQueueSize  int
	summaryJobTimeout time.Duration

	skipDBInit  bool
	tablePrefix string
	schema      string

	// Hooks for session operations.
	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook

	// Vector index settings.
	indexDimension int
	maxResults     int
	hnswM          int
	hnswEf         int
	hybridRRFK     int
	candidateRatio int

	// Embedder generates event embeddings.
	embedder     embedder.Embedder
	embedTimeout time.Duration
	// syncIndexing forces embedding generation to happen
	// in the caller/worker path instead of a detached
	// goroutine.
	syncIndexing bool
	// indexTextBuilder customizes the stored searchable
	// text before embedding.
	indexTextBuilder IndexTextBuilder
}

// ServiceOpt is a functional option for the pgvector
// session service.
type ServiceOpt func(*ServiceOpts)

var defaultOptions = ServiceOpts{
	sessionEventLimit:  defaultSessionEventLimit,
	asyncPersisterNum:  defaultAsyncPersisterNum,
	enableAsyncPersist: false,
	asyncSummaryNum:    defaultAsyncSummaryNum,
	summaryQueueSize:   defaultSummaryQueueSize,
	summaryJobTimeout:  defaultSummaryJobTimeout,
	softDelete:         true,
	indexDimension:     defaultIndexDimension,
	maxResults:         defaultMaxResults,
	hnswM:              defaultHNSWM,
	hnswEf:             defaultHNSWEf,
	hybridRRFK:         defaultHybridRRFK,
	candidateRatio:     defaultCandidateRatio,
	embedTimeout:       defaultEmbedTimeout,
}

// WithPostgresClientDSN sets the PostgreSQL DSN connection
// string directly (recommended).
func WithPostgresClientDSN(dsn string) ServiceOpt {
	return func(o *ServiceOpts) { o.dsn = dsn }
}

// WithHost sets the PostgreSQL host.
func WithHost(host string) ServiceOpt {
	return func(o *ServiceOpts) { o.host = host }
}

// WithPort sets the PostgreSQL port.
func WithPort(port int) ServiceOpt {
	return func(o *ServiceOpts) { o.port = port }
}

// WithUser sets the username for authentication.
func WithUser(user string) ServiceOpt {
	return func(o *ServiceOpts) { o.user = user }
}

// WithPassword sets the password for authentication.
func WithPassword(password string) ServiceOpt {
	return func(o *ServiceOpts) { o.password = password }
}

// WithDatabase sets the database name.
func WithDatabase(database string) ServiceOpt {
	return func(o *ServiceOpts) { o.database = database }
}

// WithSSLMode sets the SSL mode for connection.
func WithSSLMode(sslMode string) ServiceOpt {
	return func(o *ServiceOpts) { o.sslMode = sslMode }
}

// WithPostgresInstance uses a named postgres instance
// from storage.
func WithPostgresInstance(name string) ServiceOpt {
	return func(o *ServiceOpts) { o.instanceName = name }
}

// WithExtraOptions sets extra options for the postgres
// client builder.
func WithExtraOptions(extra ...any) ServiceOpt {
	return func(o *ServiceOpts) {
		o.extraOptions = append(o.extraOptions, extra...)
	}
}

// WithSessionEventLimit sets the limit of events in a
// session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(o *ServiceOpts) {
		o.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state and
// event list.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(o *ServiceOpts) { o.sessionTTL = ttl }
}

// WithAppStateTTL sets the TTL for app state.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(o *ServiceOpts) { o.appStateTTL = ttl }
}

// WithUserStateTTL sets the TTL for user state.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(o *ServiceOpts) { o.userStateTTL = ttl }
}

// WithEnableAsyncPersist enables async persistence for
// session events.
func WithEnableAsyncPersist(enable bool) ServiceOpt {
	return func(o *ServiceOpts) {
		o.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of workers for
// async persistence.
func WithAsyncPersisterNum(num int) ServiceOpt {
	return func(o *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		o.asyncPersisterNum = num
	}
}

// WithSummarizer injects a summarizer for LLM-based
// summaries.
func WithSummarizer(s summary.SessionSummarizer) ServiceOpt {
	return func(o *ServiceOpts) { o.summarizer = s }
}

// WithAsyncSummaryNum sets the number of workers for
// async summary processing.
func WithAsyncSummaryNum(num int) ServiceOpt {
	return func(o *ServiceOpts) {
		if num < 1 {
			num = defaultAsyncSummaryNum
		}
		o.asyncSummaryNum = num
	}
}

// WithSummaryQueueSize sets the size of the summary job
// queue.
func WithSummaryQueueSize(size int) ServiceOpt {
	return func(o *ServiceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		o.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing
// a single summary job.
func WithSummaryJobTimeout(
	timeout time.Duration,
) ServiceOpt {
	return func(o *ServiceOpts) {
		if timeout <= 0 {
			return
		}
		o.summaryJobTimeout = timeout
	}
}

// WithSoftDelete enables or disables soft delete.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(o *ServiceOpts) { o.softDelete = enable }
}

// WithCleanupInterval sets the interval for automatic
// cleanup of expired data.
func WithCleanupInterval(
	interval time.Duration,
) ServiceOpt {
	return func(o *ServiceOpts) {
		o.cleanupInterval = interval
	}
}

// WithSkipDBInit skips database initialization.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(o *ServiceOpts) { o.skipDBInit = skip }
}

// WithTablePrefix sets a prefix for all table names.
func WithTablePrefix(prefix string) ServiceOpt {
	return func(o *ServiceOpts) {
		if prefix == "" {
			o.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		if !strings.HasSuffix(prefix, "_") {
			prefix += "_"
		}
		o.tablePrefix = prefix
	}
}

// WithSchema sets the PostgreSQL schema name.
func WithSchema(schema string) ServiceOpt {
	return func(o *ServiceOpts) {
		if schema != "" {
			sqldb.MustValidateTableName(schema)
		}
		o.schema = schema
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(
	hooks ...session.AppendEventHook,
) ServiceOpt {
	return func(o *ServiceOpts) {
		o.appendEventHooks = append(
			o.appendEventHooks, hooks...,
		)
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(
	hooks ...session.GetSessionHook,
) ServiceOpt {
	return func(o *ServiceOpts) {
		o.getSessionHooks = append(
			o.getSessionHooks, hooks...,
		)
	}
}

// WithEmbedder sets the embedder for generating event
// embeddings. Required for pgvector service
// initialization and vector search support.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(o *ServiceOpts) { o.embedder = e }
}

// WithEmbedTimeout sets the timeout for embedding API calls.
// Default is 30 seconds. Increase this if you experience
// timeout errors with slow embedding APIs.
func WithEmbedTimeout(timeout time.Duration) ServiceOpt {
	return func(o *ServiceOpts) {
		if timeout > 0 {
			o.embedTimeout = timeout
		}
	}
}

// WithSyncIndexing controls whether event embeddings are
// generated synchronously after persistence.
func WithSyncIndexing(sync bool) ServiceOpt {
	return func(o *ServiceOpts) {
		o.syncIndexing = sync
	}
}

// WithIndexTextBuilder customizes the text used for
// event embeddings.
func WithIndexTextBuilder(builder IndexTextBuilder) ServiceOpt {
	return func(o *ServiceOpts) {
		o.indexTextBuilder = builder
	}
}

// WithIndexDimension sets the embedding vector dimension
// (default: 1536). It must match the configured embedder
// dimension when the embedder reports one.
func WithIndexDimension(dim int) ServiceOpt {
	return func(o *ServiceOpts) {
		if dim > 0 {
			o.indexDimension = dim
		}
	}
}

// WithMaxResults sets the default max results for
// SearchEvents (default: 5).
func WithMaxResults(n int) ServiceOpt {
	return func(o *ServiceOpts) {
		if n > 0 {
			o.maxResults = n
		}
	}
}

// WithHNSWM sets the HNSW index M parameter
// (default: 16).
func WithHNSWM(m int) ServiceOpt {
	return func(o *ServiceOpts) {
		if m > 0 {
			o.hnswM = m
		}
	}
}

// WithHNSWEfConstruction sets the HNSW index
// ef_construction parameter (default: 200).
func WithHNSWEfConstruction(ef int) ServiceOpt {
	return func(o *ServiceOpts) {
		if ef > 0 {
			o.hnswEf = ef
		}
	}
}

// WithHybridRRFK sets the RRF constant used when
// SearchModeHybrid is enabled (default: 60).
func WithHybridRRFK(k int) ServiceOpt {
	return func(o *ServiceOpts) {
		if k > 0 {
			o.hybridRRFK = k
		}
	}
}

// WithHybridCandidateRatio sets how many candidates each
// hybrid branch fetches before fusion (default: 3x).
func WithHybridCandidateRatio(ratio int) ServiceOpt {
	return func(o *ServiceOpts) {
		if ratio > 0 {
			o.candidateRatio = ratio
		}
	}
}
