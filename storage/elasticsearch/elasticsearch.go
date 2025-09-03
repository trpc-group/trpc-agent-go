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
	"context"
	"fmt"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
)

// Client defines the minimal interface for Elasticsearch operations.
// Use []byte payloads to decouple from SDK typed APIs.
type Client interface {
	// Ping checks if Elasticsearch is available.
	Ping(ctx context.Context) error
	// CreateIndex creates an index with the provided body.
	CreateIndex(ctx context.Context, indexName string, body []byte) error
	// DeleteIndex deletes the specified index.
	DeleteIndex(ctx context.Context, indexName string) error
	// IndexExists returns whether the specified index exists.
	IndexExists(ctx context.Context, indexName string) (bool, error)
	// IndexDoc indexes a document with the given identifier.
	IndexDoc(ctx context.Context, indexName, id string, body []byte) error
	// GetDoc retrieves a document by identifier and returns the raw body.
	GetDoc(ctx context.Context, indexName, id string) ([]byte, error)
	// UpdateDoc applies a partial update to the document by identifier.
	UpdateDoc(ctx context.Context, indexName, id string, body []byte) error
	// DeleteDoc deletes a document by identifier.
	DeleteDoc(ctx context.Context, indexName, id string) error
	// Search executes a query and returns the raw response body.
	Search(ctx context.Context, indexName string, body []byte) ([]byte, error)
}

// DefaultClientBuilder selects implementation by Version and builds a client.
func DefaultClientBuilder(builderOpts ...ClientBuilderOpt) (Client, error) {
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
func NewClient(client any) (Client, error) {
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
