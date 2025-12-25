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
	"sync"

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

// ClientBuilder is a function that creates a new Qdrant client.
type ClientBuilder func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error)

var (
	builderMu     sync.RWMutex
	globalBuilder ClientBuilder = defaultClientBuilder
)

// SetClientBuilder sets the global Qdrant client builder.
// This can be used for testing or to provide a custom client implementation.
func SetClientBuilder(builder ClientBuilder) {
	builderMu.Lock()
	defer builderMu.Unlock()
	globalBuilder = builder
}

// GetClientBuilder returns the global Qdrant client builder.
func GetClientBuilder() ClientBuilder {
	builderMu.RLock()
	defer builderMu.RUnlock()
	return globalBuilder
}

// defaultClientBuilder is the default client builder for Qdrant.
func defaultClientBuilder(_ context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	opts := &ClientBuilderOpts{
		Host: defaultHost,
		Port: defaultPort,
	}
	for _, opt := range builderOpts {
		opt(opts)
	}

	if opts.Host == "" {
		return nil, ErrEmptyHost
	}

	config := &qdrant.Config{
		Host:   opts.Host,
		Port:   opts.Port,
		APIKey: opts.APIKey,
		UseTLS: opts.UseTLS,
	}

	return qdrant.NewClient(config)
}
