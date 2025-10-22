//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysql provides the mysql instance info management.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func init() {
	mysqlRegistry = make(map[string][]ClientBuilderOpt)
}

var mysqlRegistry map[string][]ClientBuilderOpt

// ClientInterface defines the interface for database operations.
// This interface abstracts the common database operations needed by the
// memory service, making it easier to inject mock implementations for testing.
type ClientInterface interface {
	// ExecContext executes a query without returning any rows.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)

	// QueryContext executes a query that returns rows.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)

	// QueryRowContext executes a query that is expected to return at most one row.
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row

	// Ping verifies a connection to the database is still alive.
	Ping() error

	// Close closes the database connection.
	Close() error
}

type clientBuilder func(builderOpts ...ClientBuilderOpt) (ClientInterface, error)

var globalBuilder clientBuilder = DefaultClientBuilder

// SetClientBuilder sets the mysql client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the mysql client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// DefaultClientBuilder is the default mysql client builder.
func DefaultClientBuilder(builderOpts ...ClientBuilderOpt) (ClientInterface, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.DSN == "" {
		return nil, errors.New("mysql: dsn is empty")
	}

	db, err := sql.Open("mysql", o.DSN)
	if err != nil {
		return nil, fmt.Errorf("mysql: open connection %s: %w", o.DSN, err)
	}

	// Set connection pool settings if provided.
	if o.MaxOpenConns > 0 {
		db.SetMaxOpenConns(o.MaxOpenConns)
	}
	if o.MaxIdleConns > 0 {
		db.SetMaxIdleConns(o.MaxIdleConns)
	}
	if o.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(o.ConnMaxLifetime)
	}
	if o.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	}

	// Test connection.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("mysql: ping failed: %w", err)
	}

	return db, nil
}

// ClientBuilderOpt is the option for the mysql client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the mysql client.
type ClientBuilderOpts struct {
	// DSN is the mysql data source name for clientBuilder.
	// Format: [username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
	// Example: user:password@tcp(localhost:3306)/dbname?parseTime=true
	DSN string

	// MaxOpenConns is the maximum number of open connections to the database.
	MaxOpenConns int

	// MaxIdleConns is the maximum number of connections in the idle connection pool.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime is the maximum amount of time a connection may be idle.
	ConnMaxIdleTime time.Duration

	// ExtraOptions is the extra options for the mysql client.
	ExtraOptions []any
}

// WithClientBuilderDSN sets the mysql client DSN for clientBuilder.
func WithClientBuilderDSN(dsn string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.DSN = dsn
	}
}

// WithMaxOpenConns sets the maximum number of open connections to the database.
func WithMaxOpenConns(n int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.MaxOpenConns = n
	}
}

// WithMaxIdleConns sets the maximum number of connections in the idle connection pool.
func WithMaxIdleConns(n int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.MaxIdleConns = n
	}
}

// WithConnMaxLifetime sets the maximum amount of time a connection may be reused.
func WithConnMaxLifetime(d time.Duration) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ConnMaxLifetime = d
	}
}

// WithConnMaxIdleTime sets the maximum amount of time a connection may be idle.
func WithConnMaxIdleTime(d time.Duration) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ConnMaxIdleTime = d
	}
}

// WithExtraOptions sets the mysql client extra options for clientBuilder.
// this option mainly used for the customized mysql client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterMySQLInstance registers a mysql instance options.
func RegisterMySQLInstance(name string, opts ...ClientBuilderOpt) {
	mysqlRegistry[name] = append(mysqlRegistry[name], opts...)
}

// GetMySQLInstance gets the mysql instance options.
func GetMySQLInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := mysqlRegistry[name]; !ok {
		return nil, false
	}
	return mysqlRegistry[name], true
}
