//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package qdrant provides a Qdrant-based implementation of the VectorStore interface.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/qdrant/go-client/qdrant"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	qdrantstorage "trpc.group/trpc-go/trpc-agent-go/storage/qdrant"
)

var _ vectorstore.VectorStore = (*VectorStore)(nil)

// VectorStore implements vectorstore.VectorStore using Qdrant.
type VectorStore struct {
	client          qdrantstorage.Client
	ownsClient      bool
	opts            options
	filterConverter searchfilter.Converter[*qdrant.Filter]
	retryCfg        retryConfig
	closeOnce       sync.Once
}

// New creates a new Qdrant VectorStore.
func New(ctx context.Context, opts ...Option) (*VectorStore, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Validate configuration
	if o.dimension <= 0 {
		return nil, fmt.Errorf("%w: dimension must be positive, got %d", ErrInvalidConfig, o.dimension)
	}
	if o.collectionName == "" {
		return nil, fmt.Errorf("%w: collection name is required", ErrInvalidConfig)
	}

	client := o.client
	ownsClient := false
	if client == nil {
		var err error
		client, err = qdrantstorage.NewClient(ctx, o.clientBuilderOpts...)
		if err != nil {
			return nil, errors.Join(ErrConnectionFailed, err)
		}
		ownsClient = true
	}

	vs := &VectorStore{
		client:          client,
		ownsClient:      ownsClient,
		opts:            o,
		filterConverter: newFilterConverter(),
		retryCfg: retryConfig{
			maxRetries:     o.maxRetries,
			baseRetryDelay: o.baseRetryDelay,
			maxRetryDelay:  o.maxRetryDelay,
		},
	}

	if err := vs.ensureCollection(ctx); err != nil {
		if ownsClient {
			_ = client.Close()
		}
		return nil, err
	}

	return vs, nil
}

// Close closes the connection if it was created by the VectorStore.
// Safe to call multiple times; only the first call closes the client.
func (vs *VectorStore) Close() error {
	if vs.client == nil || !vs.ownsClient {
		return nil
	}
	var err error
	vs.closeOnce.Do(func() {
		err = vs.client.Close()
	})
	return err
}
