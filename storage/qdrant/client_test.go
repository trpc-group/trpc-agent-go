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

func TestGetClientBuilder(t *testing.T) {
	t.Parallel()
	builder := GetClientBuilder()
	assert.NotNil(t, builder)
}

func TestSetClientBuilder(t *testing.T) {
	original := GetClientBuilder()
	defer SetClientBuilder(original)

	called := false
	customBuilder := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		called = true
		return nil, nil
	}

	SetClientBuilder(customBuilder)

	builder := GetClientBuilder()
	_, _ = builder(context.Background())
	assert.True(t, called)
}

func TestDefaultClientBuilderWithOptions(t *testing.T) {
	t.Parallel()

	// Test that defaultClientBuilder applies options correctly
	// We can't fully test without a real Qdrant server, but we can verify
	// the client is created (connection happens lazily)
	client, err := defaultClientBuilder(context.Background(),
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

func TestDefaultClientBuilderWithTLS(t *testing.T) {
	t.Parallel()

	// Test with TLS enabled
	client, err := defaultClientBuilder(context.Background(),
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

func TestDefaultClientBuilderDefaults(t *testing.T) {
	t.Parallel()

	// Test with no options (uses defaults)
	client, err := defaultClientBuilder(context.Background())

	assert.NoError(t, err)
	assert.NotNil(t, client)

	if client != nil {
		_ = client.Close()
	}
}
