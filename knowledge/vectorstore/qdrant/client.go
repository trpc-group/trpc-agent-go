//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"

	"github.com/qdrant/go-client/qdrant"
)

// Client defines the interface for Qdrant client operations.
// This allows for mocking in tests.
type Client interface {
	CollectionExists(ctx context.Context, collectionName string) (bool, error)
	CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error
	Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	Get(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error)
	Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	Count(ctx context.Context, req *qdrant.CountPoints) (uint64, error)
	Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error)
	Close() error
}

// NewClient creates a new Qdrant client.
// The returned *qdrant.Client implements the Client interface.
func NewClient(config *qdrant.Config) (Client, error) {
	return qdrant.NewClient(config)
}
