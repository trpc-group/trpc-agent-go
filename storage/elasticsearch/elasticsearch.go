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

import (
	"fmt"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
)

// defaultClientBuilder selects implementation by Version and builds a client.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (any, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	switch o.Version {
	case ESVersionV7:
		return newClientV7(o)
	case ESVersionV8:
		return newClientV8(o)
	case ESVersionV9, ESVersionUnspecified:
		return newClientV9(o)
	default:
		return nil, fmt.Errorf("elasticsearch: unknown version %s", o.Version)
	}
}

// NewClient wraps a specific go-elasticsearch client (*v7/*v8/*v9) and returns
// a storage-level Client adapter.
func NewClient(client any) (any, error) {
	switch cli := client.(type) {
	case *esv7.Client:
		return &clientV7{esClient: cli}, nil
	case *esv8.Client:
		return &clientV8{esClient: cli}, nil
	case *esv9.Client:
		return &clientV9{esClient: cli}, nil
	default:
		return nil, fmt.Errorf("elasticsearch: unsupported client type %T", client)
	}
}
