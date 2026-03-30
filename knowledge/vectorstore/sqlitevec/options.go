//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sqlitevec provides a sqlite-vec-backed implementation of the
// knowledge vector store.
package sqlitevec

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

const (
	defaultDriverName        = "sqlite3"
	defaultDSN               = ":memory:"
	defaultTableName         = "knowledge_documents"
	defaultMetadataTableName = "knowledge_document_meta"
	defaultIndexDimension    = 1536
	defaultMaxResults        = 10
)

type options struct {
	dsn               string
	driverName        string
	tableName         string
	metadataTableName string
	indexDimension    int
	maxResults        int
	skipDBInit        bool
}

var defaultOptions = options{
	dsn:               defaultDSN,
	driverName:        defaultDriverName,
	tableName:         defaultTableName,
	metadataTableName: defaultMetadataTableName,
	indexDimension:    defaultIndexDimension,
	maxResults:        defaultMaxResults,
}

// Option configures the sqlitevec vector store.
type Option func(*options)

// WithDSN sets the SQLite DSN used when opening a database internally.
// Common mattn/go-sqlite3 DSN examples:
//   - ":memory:" for an in-memory database
//   - "file:/tmp/knowledge.db?_busy_timeout=5000" for a local file
//   - "file::memory:?cache=shared" for a shared in-memory database
func WithDSN(dsn string) Option {
	return func(o *options) {
		if dsn != "" {
			o.dsn = dsn
		}
	}
}

// WithDriverName sets the SQL driver name used with WithDSN.
func WithDriverName(driverName string) Option {
	return func(o *options) {
		if driverName != "" {
			o.driverName = driverName
		}
	}
}

// WithTableName sets the vec0 table name.
func WithTableName(tableName string) Option {
	return func(o *options) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid table name: %v", err))
		}
		o.tableName = tableName
	}
}

// WithMetadataTableName sets the metadata index table name.
func WithMetadataTableName(tableName string) Option {
	return func(o *options) {
		if err := sqldb.ValidateTableName(tableName); err != nil {
			panic(fmt.Sprintf("invalid metadata table name: %v", err))
		}
		o.metadataTableName = tableName
	}
}

// WithIndexDimension sets the embedding dimension.
func WithIndexDimension(dimension int) Option {
	return func(o *options) {
		if dimension > 0 {
			o.indexDimension = dimension
		}
	}
}

// WithMaxResults sets the default search result limit.
func WithMaxResults(maxResults int) Option {
	return func(o *options) {
		if maxResults > 0 {
			o.maxResults = maxResults
		}
	}
}

// WithSkipDBInit skips schema initialization.
func WithSkipDBInit(skip bool) Option {
	return func(o *options) {
		o.skipDBInit = skip
	}
}
