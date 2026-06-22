//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	defaultDatabase              = "trpc-agent-go-mongo-session"
	defaultSessionEventLimit     = 1000
	defaultChanBufferSize        = 100
	defaultAsyncPersisterNum     = 10
	defaultCleanupIntervalSecond = 5 * time.Minute
	defaultAsyncPersistTimeout   = 5 * time.Second
	defaultAsyncSummaryNum       = 3
	defaultSummaryQueueSize      = 100
	defaultSummaryJobTimeout     = 60 * time.Second
	collectionNameSessionTracks  = "session_tracks"
)

// serviceOpts is the options for the mongodb session service.
type serviceOpts struct {
	// MongoDB connection settings.
	uri          string
	database     string
	instanceName string
	extraOptions []any

	sessionEventLimit int           // limit of events returned per session in context-window mode
	sessionTTL        time.Duration // TTL for session state
	appStateTTL       time.Duration // TTL for app state
	userStateTTL      time.Duration // TTL for user state

	enableAsyncPersist bool
	asyncPersisterNum  int
	cleanupInterval    time.Duration // interval for session_events / session_tracks cleanup ticker
	softDelete         bool

	skipDBInit       bool
	collectionPrefix string

	// Summary options. When a summarizer is configured, NewService starts an
	// async summary worker using these settings.
	summarizer                summary.SessionSummarizer
	asyncSummaryNum           int
	summaryQueueSize          int
	summaryJobTimeout         time.Duration
	summaryFilterAllowlist    []string
	cascadeFullSessionSummary *bool

	appendEventHooks []session.AppendEventHook
	getSessionHooks  []session.GetSessionHook
}

// ServiceOpt is the option for the mongodb session service.
type ServiceOpt func(*serviceOpts)

var defaultOptions = serviceOpts{
	sessionEventLimit:  defaultSessionEventLimit,
	asyncPersisterNum:  defaultAsyncPersisterNum,
	enableAsyncPersist: false,
	asyncSummaryNum:    defaultAsyncSummaryNum,
	summaryQueueSize:   defaultSummaryQueueSize,
	summaryJobTimeout:  defaultSummaryJobTimeout,
	cleanupInterval:    0,
	softDelete:         true,
}

func (opts serviceOpts) shouldCascadeFullSessionSummary() bool {
	if opts.cascadeFullSessionSummary == nil {
		return true
	}
	return *opts.cascadeFullSessionSummary
}

// WithMongoClientURI sets the MongoDB connection URI directly (recommended).
// Example: "mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0"
//
// Note: WithMongoClientURI has the highest priority.
// If both WithMongoClientURI and WithMongoInstance are specified, the URI is used.
func WithMongoClientURI(uri string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.uri = uri
	}
}

// WithMongoInstance uses a mongodb instance previously registered via
// storage/mongodb.RegisterMongoDBInstance.
// Direct WithMongoClientURI takes precedence over WithMongoInstance.
func WithMongoInstance(instanceName string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.instanceName = instanceName
	}
}

// WithDatabase sets the MongoDB database name. If unset, a default is used.
func WithDatabase(database string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.database = database
	}
}

// WithExtraOptions sets the extra options for the mongodb session service.
// This option is mainly used by customized client builders, it will be passed
// through verbatim and ignored by the default builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionEventLimit sets the upper bound on events returned by GetSession
// / ListSessions in context-window mode. Default: 1000.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state.
// If not set, sessions will not expire.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
// If not set, app state will not expire.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
// If not set, user state will not expire.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithEnableAsyncPersist enables async persistence for session state and events.
// If not set, AppendEvent persists synchronously.
func WithEnableAsyncPersist(enable bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.enableAsyncPersist = enable
	}
}

// WithAsyncPersisterNum sets the number of workers for async event persistence.
// Values below 1 fall back to the default.
func WithAsyncPersisterNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num < 1 {
			num = defaultAsyncPersisterNum
		}
		opts.asyncPersisterNum = num
	}
}

// WithCleanupInterval sets the session_events cleanup interval.
// If session TTL is configured and this option is left as zero, NewService uses
// the default cleanup interval. Other collections rely on MongoDB TTL indexes.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.cleanupInterval = interval
	}
}

// WithSoftDelete toggles soft delete mode.
// Default: true. When enabled, DeleteSession / DeleteAppState / DeleteUserState
// set deleted_at instead of removing the document.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.softDelete = enable
	}
}

// WithSkipDBInit skips collection / index initialization at NewService time.
// Useful when the user does not have createIndex permission or indexes are
// managed externally.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.skipDBInit = skip
	}
}

// WithCollectionPrefix sets a prefix applied to all collection names.
// For example, with prefix "trpc", collections become:
//   - trpc_session_states
//   - trpc_app_states
//   - trpc_user_states
//
// An underscore is appended automatically when missing. The prefix is
// validated via internal/session/sqldb.MustValidateTablePrefix.
func WithCollectionPrefix(prefix string) ServiceOpt {
	return func(opts *serviceOpts) {
		if prefix == "" {
			opts.collectionPrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		if !strings.HasSuffix(prefix, "_") {
			prefix += "_"
		}
		opts.collectionPrefix = prefix
	}
}

// WithGetSessionHook adds GetSession hooks.
func WithGetSessionHook(hooks ...session.GetSessionHook) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}

// WithAppendEventHook adds AppendEvent hooks.
func WithAppendEventHook(hooks ...session.AppendEventHook) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.appendEventHooks = append(opts.appendEventHooks, hooks...)
	}
}

// WithSummarizer injects a summarizer for LLM-based summaries.
// Without a summarizer CreateSessionSummary / EnqueueSummaryJob become no-ops.
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

// WithSummaryQueueSize sets the async summary queue size.
func WithSummaryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size < 1 {
			size = defaultSummaryQueueSize
		}
		opts.summaryQueueSize = size
	}
}

// WithSummaryJobTimeout sets the timeout for processing a single summary job.
func WithSummaryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout <= 0 {
			return
		}
		opts.summaryJobTimeout = timeout
	}
}

// WithSummaryFilterAllowlist restricts which non-empty filter keys may
// trigger branch summaries. Keys use the same exact format as event filter
// keys.
func WithSummaryFilterAllowlist(filterKeys ...string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.summaryFilterAllowlist = append([]string{}, filterKeys...)
	}
}

// WithCascadeFullSessionSummary controls whether an allowed branch summary
// also refreshes the full-session summary keyed by SummaryFilterKeyAllContents.
func WithCascadeFullSessionSummary(enable bool) ServiceOpt {
	return func(opts *serviceOpts) {
		v := enable
		opts.cascadeFullSessionSummary = &v
	}
}
