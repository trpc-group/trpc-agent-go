//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface, implementation and options.
package elasticsearch

// Registry and builder alignment to match other storage modules.

func init() {
	esRegistry = make(map[string][]ClientBuilderOpt)
}

// esRegistry stores named Elasticsearch instance builder options.
var esRegistry map[string][]ClientBuilderOpt

// clientBuilder builds an Elasticsearch Client from builder options.
type clientBuilder func(builderOpts ...ClientBuilderOpt) (Client, error)

// clientBuilder is the function to build the global Elasticsearch client.
var globalBuilder clientBuilder = DefaultClientBuilder

// SetClientBuilder sets the global Elasticsearch client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the global Elasticsearch client builder.
func GetClientBuilder() clientBuilder { return globalBuilder }

// RegisterElasticsearchInstance registers a named Elasticsearch instance options.
func RegisterElasticsearchInstance(name string, opts ...ClientBuilderOpt) {
	esRegistry[name] = append(esRegistry[name], opts...)
}

// GetElasticsearchInstance gets the registered options for a named instance.
func GetElasticsearchInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := esRegistry[name]; !ok {
		return nil, false
	}
	return esRegistry[name], true
}

// ClientBuilderOpt is the option for the Elasticsearch client builder.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the Elasticsearch client builder.
type ClientBuilderOpts struct {
	// Version allows selecting the target Elasticsearch major version.
	// Defaults to ESVersionUnspecified which implies auto or default.
	Version ESVersion

	// ExtraOptions allows custom builders to accept extra parameters.
	ExtraOptions []any
}

// WithExtraOptions adds extra, builder-specific options.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// ESVersion represents the Elasticsearch major version.
type ESVersion int

const (
	// ESVersionUnspecified means no explicit version preference.
	ESVersionUnspecified ESVersion = 0
	// ESVersionV7 selects Elasticsearch v7.
	ESVersionV7 ESVersion = 7
	// ESVersionV8 selects Elasticsearch v8.
	ESVersionV8 ESVersion = 8
)

// WithVersion sets the preferred Elasticsearch major version.
func WithVersion(v ESVersion) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Version = v }
}
