//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mysql provides a MySQL-backed PromptIter store implementation.
package mysql

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const defaultInitTimeout = 30 * time.Second

type options struct {
	// dsn is the MySQL DSN connection string.
	dsn string
	// instanceName is the registered MySQL instance name used when dsn is empty.
	instanceName string
	// extraOptions contains extra options passed to the storage MySQL client builder.
	extraOptions []any
	// skipDBInit indicates whether database schema initialization is skipped.
	skipDBInit bool
	// tablePrefix is the prefix applied to all table names.
	tablePrefix string
	// initTimeout is the timeout used for database schema initialization.
	initTimeout time.Duration
}

// Option configures the MySQL PromptIter store.
type Option func(*options)

func newOptions(opts ...Option) *options {
	options := &options{
		initTimeout: defaultInitTimeout,
	}
	for _, opt := range opts {
		opt(options)
	}
	return options
}

// WithMySQLClientDSN sets the MySQL DSN connection string directly.
func WithMySQLClientDSN(dsn string) Option {
	return func(options *options) {
		options.dsn = dsn
	}
}

// WithMySQLInstance uses a registered MySQL instance when the DSN is empty.
func WithMySQLInstance(instanceName string) Option {
	return func(options *options) {
		options.instanceName = instanceName
	}
}

// WithExtraOptions sets extra options passed to the MySQL client builder.
func WithExtraOptions(extraOptions ...any) Option {
	return func(options *options) {
		options.extraOptions = append(options.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips table and index initialization.
func WithSkipDBInit(skip bool) Option {
	return func(options *options) {
		options.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
func WithTablePrefix(prefix string) Option {
	return func(options *options) {
		if prefix == "" {
			options.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		options.tablePrefix = prefix
	}
}

// WithInitTimeout sets the timeout used for schema initialization.
func WithInitTimeout(timeout time.Duration) Option {
	return func(options *options) {
		if timeout <= 0 {
			return
		}
		options.initTimeout = timeout
	}
}
