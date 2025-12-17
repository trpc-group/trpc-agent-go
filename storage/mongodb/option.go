//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mongodb provides the MongoDB instance info management.
package mongodb

// ClientBuilderOpt is the option for the mongodb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the mongodb client.
type ClientBuilderOpts struct {
	// URI is the mongodb connection string.
	// Format: "mongodb://username:password@host:port/database?options"
	URI string

	// ExtraOptions is the extra options for the redis client.
	ExtraOptions []any
}

// WithClientBuilderDSN sets the mongodb connection URI for clientBuilder.
func WithClientBuilderDSN(uri string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.URI = uri
	}
}

// WithExtraOptions sets the mongodb client extra options for clientBuilder.
// This option is mainly used for customized mongodb client builders.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}
