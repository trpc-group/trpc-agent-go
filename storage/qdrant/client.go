//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package qdrant provides a reusable Qdrant client for storage operations.
package qdrant

import (
	"context"

	"github.com/qdrant/go-client/qdrant"
)

// Client defines the interface for Qdrant client operations.
// This allows for mocking in tests.
type Client interface {
	CollectionExists(ctx context.Context, collectionName string) (bool, error)
	GetCollectionInfo(ctx context.Context, collectionName string) (*qdrant.CollectionInfo, error)
	CreateCollection(ctx context.Context, req *qdrant.CreateCollection) error
	DeleteCollection(ctx context.Context, collectionName string) error
	CreateFieldIndex(ctx context.Context, req *qdrant.CreateFieldIndexCollection) (*qdrant.UpdateResult, error)
	Upsert(ctx context.Context, req *qdrant.UpsertPoints) (*qdrant.UpdateResult, error)
	Get(ctx context.Context, req *qdrant.GetPoints) ([]*qdrant.RetrievedPoint, error)
	Delete(ctx context.Context, req *qdrant.DeletePoints) (*qdrant.UpdateResult, error)
	SetPayload(ctx context.Context, req *qdrant.SetPayloadPoints) (*qdrant.UpdateResult, error)
	Query(ctx context.Context, req *qdrant.QueryPoints) ([]*qdrant.ScoredPoint, error)
	Count(ctx context.Context, req *qdrant.CountPoints) (uint64, error)
	Scroll(ctx context.Context, req *qdrant.ScrollPoints) ([]*qdrant.RetrievedPoint, error)
	Close() error
}

// NewClient creates a new Qdrant client with the given options.
//
// Example:
//
//	client, err := qdrant.NewClient(ctx,
//	    qdrant.WithHost("localhost"),
//	    qdrant.WithPort(6334),
//	)
func NewClient(_ context.Context, opts ...ClientBuilderOpt) (Client, error) {
	cfg := &ClientBuilderOpts{
		Host: defaultHost,
		Port: defaultPort,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.Host == "" {
		return nil, ErrEmptyHost
	}

	config := &qdrant.Config{
		Host:   cfg.Host,
		Port:   cfg.Port,
		APIKey: cfg.APIKey,
		UseTLS: cfg.UseTLS,
	}

	return qdrant.NewClient(config)
}
