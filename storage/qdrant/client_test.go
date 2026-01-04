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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewClientWithOptions(t *testing.T) {
	t.Parallel()

	// Test that NewClient applies options correctly
	// We can't fully test without a real Qdrant server, but we can verify
	// the client is created (connection happens lazily)
	client, err := NewClient(context.Background(),
		WithHost("localhost"),
		WithPort(6334),
		WithAPIKey("test-key"),
		WithTLS(false),
	)

	assert.NoError(t, err)
	assert.NotNil(t, client)

	// Close the client
	if client != nil {
		_ = client.Close()
	}
}

func TestNewClientWithTLS(t *testing.T) {
	t.Parallel()

	// Test with TLS enabled
	client, err := NewClient(context.Background(),
		WithHost("localhost"),
		WithPort(6334),
		WithTLS(true),
	)

	// Client creation should succeed (connection is lazy)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	if client != nil {
		_ = client.Close()
	}
}

func TestNewClientDefaults(t *testing.T) {
	t.Parallel()

	// Test with no options (uses defaults)
	client, err := NewClient(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, client)

	if client != nil {
		_ = client.Close()
	}
}
