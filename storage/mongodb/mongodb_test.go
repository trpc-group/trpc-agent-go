//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndGetMongoDBInstance(t *testing.T) {
	// Clean up registry
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	// Register an instance
	RegisterMongoDBInstance("test-instance", WithClientBuilderDSN("mongodb://localhost:27017"))

	// Get the instance
	opts, ok := GetMongoDBInstance("test-instance")
	assert.True(t, ok)
	assert.Len(t, opts, 1)

	// Get non-existent instance
	_, ok = GetMongoDBInstance("non-existent")
	assert.False(t, ok)
}

func TestRegisterMongoDBInstanceAppend(t *testing.T) {
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	RegisterMongoDBInstance("test", WithClientBuilderDSN("mongodb://localhost:27017"))
	RegisterMongoDBInstance("test", WithExtraOptions("extra"))

	opts, ok := GetMongoDBInstance("test")
	assert.True(t, ok)
	assert.Len(t, opts, 2)
}

func TestSetAndGetClientBuilder(t *testing.T) {
	// Save original builder
	original := globalBuilder
	defer func() { globalBuilder = original }()

	// Set custom builder
	customBuilder := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return nil, errors.New("custom builder")
	}
	SetClientBuilder(customBuilder)

	// Get builder
	builder := GetClientBuilder()
	assert.NotNil(t, builder)

	// Verify it's the custom builder
	_, err := builder(context.Background())
	assert.EqualError(t, err, "custom builder")
}

func TestClientBuilderOpts(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithClientBuilderDSN("mongodb://localhost:27017")(opts)
	assert.Equal(t, "mongodb://localhost:27017", opts.URI)

	WithExtraOptions("opt1", "opt2")(opts)
	assert.Len(t, opts.ExtraOptions, 2)
	assert.Equal(t, "opt1", opts.ExtraOptions[0])
	assert.Equal(t, "opt2", opts.ExtraOptions[1])
}

func TestNewClientWithNilBuilder(t *testing.T) {
	original := globalBuilder
	defer func() { globalBuilder = original }()

	globalBuilder = nil

	_, err := NewClient(context.Background())
	assert.ErrorIs(t, err, ErrNoClientBuilder)
}

func TestNewClientFromInstanceWithNilBuilder(t *testing.T) {
	original := globalBuilder
	defer func() { globalBuilder = original }()

	globalBuilder = nil

	_, err := NewClientFromInstance(context.Background(), "test")
	assert.ErrorIs(t, err, ErrNoClientBuilder)
}

func TestNewClientFromInstanceNotFound(t *testing.T) {
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	_, err := NewClientFromInstance(context.Background(), "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

func TestNewClientFromInstance(t *testing.T) {
	original := globalBuilder
	defer func() { globalBuilder = original }()
	mongodbRegistry = make(map[string][]ClientBuilderOpt)

	// Register instance
	RegisterMongoDBInstance("test", WithClientBuilderDSN("mongodb://localhost:27017"))

	// Set mock builder
	var capturedOpts *ClientBuilderOpts
	SetClientBuilder(func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		capturedOpts = &ClientBuilderOpts{}
		for _, opt := range opts {
			opt(capturedOpts)
		}
		return nil, errors.New("mock")
	})

	// Call with extra options
	_, _ = NewClientFromInstance(context.Background(), "test", WithExtraOptions("extra"))

	require.NotNil(t, capturedOpts)
	assert.Equal(t, "mongodb://localhost:27017", capturedOpts.URI)
	assert.Len(t, capturedOpts.ExtraOptions, 1)
}

func TestDefaultClientBuilderEmptyURI(t *testing.T) {
	_, err := defaultClientBuilder(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "URI is empty")
}

