//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package clickhouse provides a ClickHouse-backed memory service.
package clickhouse

import (
	"fmt"
	"regexp"

	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

var defaultOptions = serviceOpts{
	tableName:        "memories",
	memoryLimit:      imemory.DefaultMemoryLimit,
	searchMinScore:   imemory.DefaultSearchMinScore,
	maxSearchResults: imemory.DefaultMaxSearchResults,
}

type serviceOpts struct {
	dsn              string
	instanceName     string
	extraOptions     []any
	tableName        string
	memoryLimit      int
	searchMinScore   float64
	maxSearchResults int
	skipDBInit       bool
}

// ServiceOpt configures a ClickHouse memory service.
type ServiceOpt func(*serviceOpts)

// WithClickHouseDSN sets the ClickHouse connection string.
// Example: clickhouse://default:@localhost:9000/default.
func WithClickHouseDSN(dsn string) ServiceOpt {
	return func(opts *serviceOpts) { opts.dsn = dsn }
}

// WithClickHouseInstance selects a ClickHouse instance registered with the
// storage/clickhouse package. A DSN takes precedence when both are provided.
func WithClickHouseInstance(instanceName string) ServiceOpt {
	return func(opts *serviceOpts) { opts.instanceName = instanceName }
}

// WithExtraOptions passes implementation-specific options to a custom
// ClickHouse client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithTableName sets the table used to store memories. The default is
// "memories". The name must start with a letter or underscore and contain only
// letters, digits, and underscores.
func WithTableName(tableName string) ServiceOpt {
	return func(opts *serviceOpts) {
		if !validTableName(tableName) {
			panic(fmt.Sprintf("invalid table name: %q", tableName))
		}
		opts.tableName = tableName
	}
}

// WithMemoryLimit sets the maximum number of active memories per user. A
// value of zero disables the limit.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		if limit >= 0 {
			opts.memoryLimit = limit
		}
	}
}

// WithMinSearchScore sets the minimum score for deterministic keyword search.
func WithMinSearchScore(score float64) ServiceOpt {
	return func(opts *serviceOpts) {
		if score >= 0 {
			opts.searchMinScore = score
		}
	}
}

// WithMaxResults sets the default maximum keyword search result count. A
// value of zero disables truncation.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *serviceOpts) {
		if maxResults >= 0 {
			opts.maxSearchResults = maxResults
		}
	}
}

// WithSkipDBInit skips schema creation. Use it only when the target table is
// managed externally.
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *serviceOpts) { opts.skipDBInit = skip }
}

var tableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validTableName(name string) bool {
	return len(name) <= 255 && tableNamePattern.MatchString(name)
}
