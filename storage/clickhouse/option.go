//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clickhouse provides the ClickHouse instance info management.
package clickhouse

func init() {
	clickhouseRegistry = make(map[string][]ClientBuilderOpt)
}

// clickhouseRegistry stores named ClickHouse instance builder options.
var clickhouseRegistry map[string][]ClientBuilderOpt

// clientBuilder builds a ClickHouse Client from builder options.
type clientBuilder func(builderOpts ...ClientBuilderOpt) (Client, error)

// globalBuilder is the function to build the global ClickHouse client.
var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the global ClickHouse client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the global ClickHouse client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// RegisterClickHouseInstance registers a named ClickHouse instance options.
func RegisterClickHouseInstance(name string, opts ...ClientBuilderOpt) {
	clickhouseRegistry[name] = append(clickhouseRegistry[name], opts...)
}

// GetClickHouseInstance gets the registered options for a named instance.
func GetClickHouseInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := clickhouseRegistry[name]; !ok {
		return nil, false
	}
	return clickhouseRegistry[name], true
}

// ClientBuilderOpt is the option for the ClickHouse client builder.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the ClickHouse client builder.
type ClientBuilderOpts struct {
	// DSN is the ClickHouse connection string.
	// Format: "clickhouse://username:password@host:port/database?options"
	// See: https://github.com/ClickHouse/clickhouse-go#dsn
	DSN string

	// ExtraOptions allows custom builders to accept extra parameters.
	ExtraOptions []any
}

// WithClientBuilderDSN sets the ClickHouse connection DSN for clientBuilder.
func WithClientBuilderDSN(dsn string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.DSN = dsn
	}
}

// WithExtraOptions sets the ClickHouse client extra options for clientBuilder.
// This option is mainly used for customized ClickHouse client builders.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.ExtraOptions = append(o.ExtraOptions, extraOptions...)
	}
}
