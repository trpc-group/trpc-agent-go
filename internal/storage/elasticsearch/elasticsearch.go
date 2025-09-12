//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface.
package elasticsearch

import (
	"context"
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
	// Count executes a count query and returns the document count.
	Count(ctx context.Context, indexName string, body []byte) (int, error)
	// DeleteByQuery deletes documents matching the query.
	DeleteByQuery(ctx context.Context, indexName string, body []byte) error
	// Refresh refreshes an index.
	Refresh(ctx context.Context, indexName string) error
}
