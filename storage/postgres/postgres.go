//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package postgres provides the PostgreSQL instance info management.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql
)

func init() {
	postgresRegistry = make(map[string][]ClientBuilderOpt)
}

var postgresRegistry map[string][]ClientBuilderOpt

type clientBuilder func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the postgres client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the postgres client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default postgres client builder.
// It creates a database/sql connection using pgx driver.
func defaultClientBuilder(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.ConnString == "" {
		return nil, errors.New("postgres: connection string is empty")
	}

	// Open database connection using pgx driver
	db, err := sql.Open("pgx", o.ConnString)
	if err != nil {
		return nil, fmt.Errorf("postgres: open connection: %w", err)
	}

	// Verify connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: ping database: %w", err)
	}

	return &sqlClient{db: db}, nil
}

// ClientBuilderOpt is the option for the postgres client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the postgres client.
type ClientBuilderOpts struct {
	// ConnString is the postgres connection string.
	// Format: "postgres://username:password@host:port/database?options"
	ConnString string

	// ExtraOptions is the extra options for the postgres client.
	// This is mainly used for customized postgres client builders.
	ExtraOptions []any
}

// WithClientConnString sets the postgres connection string for clientBuilder.
func WithClientConnString(connString string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ConnString = connString
	}
}

// WithExtraOptions sets the postgres client extra options for clientBuilder.
// This option is mainly used for customized postgres client builders.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterPostgresInstance registers a postgres instance with the given options.
func RegisterPostgresInstance(name string, opts ...ClientBuilderOpt) {
	postgresRegistry[name] = append(postgresRegistry[name], opts...)
}

// GetPostgresInstance gets the postgres instance options by name.
func GetPostgresInstance(name string) ([]ClientBuilderOpt, bool) {
	instance, ok := postgresRegistry[name]
	return instance, ok
}

// Client defines the interface for PostgreSQL operations.
// It mirrors the database/sql standard library interface.
type Client interface {
	// ExecContext executes a query that doesn't return rows.
	// For example: INSERT, UPDATE, DELETE.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)

	// Query executes a query that returns rows and passes them to the handler.
	// The rows are automatically closed after the handler returns.
	// This ensures proper resource cleanup and prevents resource leaks.
	Query(ctx context.Context, fn HandlerFunc, query string, args ...any) error

	// Transaction executes a function within a transaction.
	// The transaction is automatically committed if the function returns nil,
	// or rolled back if the function returns an error or panics.
	Transaction(ctx context.Context, fn TxFunc) error

	// Close closes the database connection pool and releases all resources.
	// After calling Close, the client should not be used anymore.
	Close() error
}

// HandlerFunc is a function that processes query results.
// The rows are automatically closed after this function returns.
type HandlerFunc func(*sql.Rows) error

// TxFunc is a function that executes within a transaction.
type TxFunc func(*sql.Tx) error

// sqlClient implements the Client interface using database/sql.
type sqlClient struct {
	db *sql.DB
}

// ExecContext executes a query that doesn't return rows.
func (c *sqlClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

// Query executes a query that returns rows and passes them to the handler.
// It automatically closes the rows after the handler completes or panics.
func (c *sqlClient) Query(ctx context.Context, handler HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	// Ensure rows are always closed, even on panic
	defer rows.Close()

	// Execute handler with rows
	if err := handler(rows); err != nil {
		return err
	}

	// Check for errors from iteration
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

// Transaction executes a function within a transaction.
// It automatically handles commit on success and rollback on error or panic.
func (c *sqlClient) Transaction(ctx context.Context, fn TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// Ensure transaction is always finalized
	defer func() {
		if p := recover(); p != nil {
			// Rollback on panic and re-panic
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			// Rollback on error
			_ = tx.Rollback()
		}
	}()

	// Execute the transaction function
	err = fn(tx)
	if err != nil {
		return err
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// Close closes the database connection pool and releases all resources.
// It's safe to call Close multiple times.
func (c *sqlClient) Close() error {
	return c.db.Close()
}
