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
)

const (
	defaultDatabase = "trpc-agent-go-mongo-session"
)

// ServiceOpts is the options for the mongodb session service.
type ServiceOpts struct {
	// MongoDB connection settings.
	uri          string
	database     string
	instanceName string
	extraOptions []any

	sessionTTL   time.Duration // TTL for session state
	appStateTTL  time.Duration // TTL for app state
	userStateTTL time.Duration // TTL for user state

	softDelete bool

	skipDBInit       bool
	collectionPrefix string

	getSessionHooks []session.GetSessionHook
}

// ServiceOpt is the option for the mongodb session service.
type ServiceOpt func(*ServiceOpts)

var defaultOptions = ServiceOpts{
	softDelete: true,
}

// WithMongoClientURI sets the MongoDB connection URI directly (recommended).
// Example: "mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0"
//
// Note: WithMongoClientURI has the highest priority.
// If both WithMongoClientURI and WithMongoInstance are specified, the URI is used.
func WithMongoClientURI(uri string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.uri = uri
	}
}

// WithMongoInstance uses a mongodb instance previously registered via
// storage/mongodb.RegisterMongoDBInstance.
// Direct WithMongoClientURI takes precedence over WithMongoInstance.
func WithMongoInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithDatabase sets the MongoDB database name. If unset, a default is used.
func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.database = database
	}
}

// WithExtraOptions sets the extra options for the mongodb session service.
// This option is mainly used by customized client builders, it will be passed
// through verbatim and ignored by the default builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session state.
// If not set, sessions will not expire.
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

// WithSoftDelete toggles soft delete mode.
// Default: true. When enabled, DeleteSession / DeleteAppState / DeleteUserState
// set deleted_at instead of removing the document.
func WithSoftDelete(enable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enable
	}
}

// WithSkipDBInit skips collection / index initialization at NewService time.
// Useful when the user does not have createIndex permission or indexes are
// managed externally.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
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
	return func(opts *ServiceOpts) {
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
	return func(opts *ServiceOpts) {
		opts.getSessionHooks = append(opts.getSessionHooks, hooks...)
	}
}
