//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysql provides a MySQL-backed evalset.Manager implementation.
package mysql

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const defaultInitTimeout = 30 * time.Second

// options holds configuration for the MySQL eval set manager.
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

// Option configures options.
type Option func(*options)

func newOptions(opts ...Option) *options {
	o := &options{
		initTimeout: defaultInitTimeout,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// WithMySQLClientDSN sets the MySQL DSN connection string directly (recommended).
func WithMySQLClientDSN(dsn string) Option {
	return func(o *options) {
		o.dsn = dsn
	}
}

// WithMySQLInstance uses a MySQL instance from storage.
// The instance must be registered via storage.RegisterMySQLInstance() before use.
func WithMySQLInstance(instanceName string) Option {
	return func(o *options) {
		o.instanceName = instanceName
	}
}

// WithExtraOptions sets extra options passed to the storage MySQL client builder.
func WithExtraOptions(extraOptions ...any) Option {
	return func(o *options) {
		o.extraOptions = append(o.extraOptions, extraOptions...)
	}
}

// WithSkipDBInit skips database initialization (table and index creation).
func WithSkipDBInit(skip bool) Option {
	return func(o *options) {
		o.skipDBInit = skip
	}
}

// WithTablePrefix sets a prefix for all table names.
//
// Security: Uses internal/session/sqldb.MustValidateTablePrefix to prevent SQL injection.
func WithTablePrefix(prefix string) Option {
	return func(o *options) {
		if prefix == "" {
			o.tablePrefix = ""
			return
		}
		sqldb.MustValidateTablePrefix(prefix)
		o.tablePrefix = prefix
	}
}

// WithInitTimeout sets the timeout for schema initialization.
func WithInitTimeout(timeout time.Duration) Option {
	return func(o *options) {
		if timeout <= 0 {
			return
		}
		o.initTimeout = timeout
	}
}
