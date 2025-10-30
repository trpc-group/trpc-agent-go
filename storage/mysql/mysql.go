//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mysql provides the mysql instance info management and client interface.
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

// Client defines the interface for database operations using callback pattern.
// This interface abstracts the common database operations needed by the
// memory service, making it easier to inject mock implementations for testing.
type Client interface {
	// Exec executes a query without returning any rows.
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)

	// Query executes a query that returns rows, calling the next function for each row.
	Query(ctx context.Context, next NextFunc, query string, args ...any) error

	// QueryRow executes a query that is expected to return at most one row and scans into dest.
	QueryRow(ctx context.Context, dest []any, query string, args ...any) error

	// Transaction executes a function within a transaction.
	Transaction(ctx context.Context, fn TxFunc, opts ...TxOption) error

	// Close closes the database connection.
	Close() error
}

// NextFunc is called for each row in a query result.
// Return ErrBreak to stop iteration early, or any other error to abort with error.
type NextFunc func(*sql.Rows) error

// TxFunc is a user transaction function.
// Return nil to commit, or any error to rollback.
type TxFunc func(*sql.Tx) error

// TxOption configures transaction options.
type TxOption func(*sql.TxOptions)

// ErrBreak can be returned from NextFunc to stop iteration early without error.
var ErrBreak = errors.New("mysql scan rows break")

// sqlDBClient wraps *sql.DB to implement the Client interface using callback pattern.
type sqlDBClient struct {
	db *sql.DB
}

// WrapSQLDB wraps a *sql.DB connection into a Client.
//
// WARNING: This function is for INTERNAL USE ONLY!
// Do NOT call this function directly from external packages.
// This is an internal implementation detail that may change without notice.
// Use the public API provided by the parent storage/mysql package instead.
//
// This function is only exported to allow access from other internal packages
// within the same module (memory/mysql, etc.).
func WrapSQLDB(db *sql.DB) Client {
	return &sqlDBClient{db: db}
}

// Exec implements Client.Exec.
func (c *sqlDBClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

// Query implements Client.Query using callback pattern.
func (c *sqlDBClient) Query(ctx context.Context, next NextFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Iterate all rows, calling the callback function.
	for rows.Next() {
		if err := next(rows); err != nil {
			if errors.Is(err, ErrBreak) {
				break
			}
			return err
		}
	}

	return rows.Err()
}

// QueryRow implements Client.QueryRow.
func (c *sqlDBClient) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	row := c.db.QueryRowContext(ctx, query, args...)
	return row.Scan(dest...)
}

// Transaction implements Client.Transaction using callback pattern.
func (c *sqlDBClient) Transaction(ctx context.Context, fn TxFunc, opts ...TxOption) error {
	txOpts := &sql.TxOptions{}
	for _, opt := range opts {
		opt(txOpts)
	}

	tx, err := c.db.BeginTx(ctx, txOpts)
	if err != nil {
		return err
	}

	// Execute user transaction function.
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Close implements Client.Close.
func (c *sqlDBClient) Close() error {
	return c.db.Close()
}

// clientBuilder is the function type for building Client instances.
type clientBuilder func(builderOpts ...ClientBuilderOpt) (Client, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the mysql client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the mysql client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default mysql client builder.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (Client, error) {
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

	return &sqlDBClient{db: db}, nil
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
