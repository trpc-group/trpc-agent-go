//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package milvus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// TestSetGetClientBuilder tests setting and getting a custom client builder
func TestSetGetClientBuilder(t *testing.T) {
	oldRegistry := milvusRegistry
	milvusRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { milvusRegistry = oldRegistry }()

	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	invoked := false
	custom := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		invoked = true
		return nil, nil
	}

	SetClientBuilder(custom)
	b := GetClientBuilder()
	_, err := b(context.Background(), WithAddress("localhost:19530"))
	require.NoError(t, err)
	require.True(t, invoked, "custom builder was not invoked")
}

// TestDefaultClientBuilder_EmptyAddress ...
func TestDefaultClientBuilder_EmptyAddress(t *testing.T) {
	const expected = "milvus address is empty"
	_, err := defaultClientBuilder(context.Background())
	require.Error(t, err)
	require.Equal(t, expected, err.Error())
}

// TestDefaultClientBuilder_ValidAddress ...
func TestDefaultClientBuilder_ValidAddress(t *testing.T) {
	t.Skip("Skipping test that requires a real Milvus connection")
}

// TestRegisterAndGetMilvusInstance ...
func TestRegisterAndGetMilvusInstance(t *testing.T) {
	oldRegistry := milvusRegistry
	milvusRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { milvusRegistry = oldRegistry }()

	const (
		name    = "test-instance"
		address = "localhost:19530"
	)

	RegisterMilvusInstance(name, WithAddress(address))
	opts, ok := GetMilvusInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, address, cfg.Address)
}

// TestGetMilvusInstance_NotFound ...
func TestGetMilvusInstance_NotFound(t *testing.T) {
	oldRegistry := milvusRegistry
	milvusRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { milvusRegistry = oldRegistry }()

	opts, ok := GetMilvusInstance("not-exist")
	require.False(t, ok)
	require.Nil(t, opts)
}

// TestWithAddress ...
func TestWithAddress(t *testing.T) {
	const address = "localhost:19530"
	cfg := &ClientBuilderOpts{}
	WithAddress(address)(cfg)
	require.Equal(t, address, cfg.Address)
}

// TestWithUsername ...
func TestWithUsername(t *testing.T) {
	const username = "testuser"
	cfg := &ClientBuilderOpts{}
	WithUsername(username)(cfg)
	require.Equal(t, username, cfg.Username)
}

// TestWithPassword ...
func TestWithPassword(t *testing.T) {
	const password = "testpass"
	cfg := &ClientBuilderOpts{}
	WithPassword(password)(cfg)
	require.Equal(t, password, cfg.Password)
}

// TestWithDBName ...
func TestWithDBName(t *testing.T) {
	const dbName = "testdb"
	cfg := &ClientBuilderOpts{}
	WithDBName(dbName)(cfg)
	require.Equal(t, dbName, cfg.DBName)
}

// TestWithAPIKey ...
func TestWithAPIKey(t *testing.T) {
	const apiKey = "test-api-key"
	cfg := &ClientBuilderOpts{}
	WithAPIKey(apiKey)(cfg)
	require.Equal(t, apiKey, cfg.APIKey)
}

// TestRegisterMilvusInstance_Overwrite ...
func TestRegisterMilvusInstance_Overwrite(t *testing.T) {
	oldRegistry := milvusRegistry
	milvusRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { milvusRegistry = oldRegistry }()

	const name = "overwrite-instance"
	RegisterMilvusInstance(name, WithAddress("localhost:19530"))
	RegisterMilvusInstance(name, WithAddress("localhost:19531"), WithUsername("user"))

	opts, ok := GetMilvusInstance(name)
	require.True(t, ok)
	require.Len(t, opts, 2)

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, "localhost:19531", cfg.Address)
	require.Equal(t, "user", cfg.Username)
}

// TestAllOptions all options together
func TestAllOptions(t *testing.T) {
	cfg := &ClientBuilderOpts{}

	WithAddress("localhost:19530")(cfg)
	WithUsername("testuser")(cfg)
	WithPassword("testpass")(cfg)
	WithDBName("testdb")(cfg)
	WithAPIKey("test-api-key")(cfg)

	require.Equal(t, "localhost:19530", cfg.Address)
	require.Equal(t, "testuser", cfg.Username)
	require.Equal(t, "testpass", cfg.Password)
	require.Equal(t, "testdb", cfg.DBName)
	require.Equal(t, "test-api-key", cfg.APIKey)
}

// TestDefaultClientBuilder_MultipleOptions builder with multiple options
func TestDefaultClientBuilder_MultipleOptions(t *testing.T) {
	opts := &ClientBuilderOpts{}

	WithAddress("localhost:19530")(opts)
	WithUsername("testuser")(opts)
	WithPassword("testpass")(opts)
	WithDBName("testdb")(opts)
	WithAPIKey("test-api-key")(opts)

	require.Equal(t, "localhost:19530", opts.Address)
	require.Equal(t, "testuser", opts.Username)
	require.Equal(t, "testpass", opts.Password)
	require.Equal(t, "testdb", opts.DBName)
	require.Equal(t, "test-api-key", opts.APIKey)
}

// TestRegisterMilvusInstance_MultipleInstances registry with multiple instances
func TestRegisterMilvusInstance_MultipleInstances(t *testing.T) {
	oldRegistry := milvusRegistry
	milvusRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { milvusRegistry = oldRegistry }()

	RegisterMilvusInstance("instance1", WithAddress("localhost:19530"))
	RegisterMilvusInstance("instance2", WithAddress("localhost:19531"))
	RegisterMilvusInstance("instance3", WithAddress("localhost:19532"))

	opts1, ok1 := GetMilvusInstance("instance1")
	require.True(t, ok1)
	require.NotEmpty(t, opts1)

	opts2, ok2 := GetMilvusInstance("instance2")
	require.True(t, ok2)
	require.NotEmpty(t, opts2)

	opts3, ok3 := GetMilvusInstance("instance3")
	require.True(t, ok3)
	require.NotEmpty(t, opts3)

	cfg1 := &ClientBuilderOpts{}
	for _, opt := range opts1 {
		opt(cfg1)
	}
	require.Equal(t, "localhost:19530", cfg1.Address)

	cfg2 := &ClientBuilderOpts{}
	for _, opt := range opts2 {
		opt(cfg2)
	}
	require.Equal(t, "localhost:19531", cfg2.Address)

	cfg3 := &ClientBuilderOpts{}
	for _, opt := range opts3 {
		opt(cfg3)
	}
	require.Equal(t, "localhost:19532", cfg3.Address)
}

// TestClientBuilderOpts_Accumulation builder options accumulation
func TestClientBuilderOpts_Accumulation(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	var capturedOpts *ClientBuilderOpts
	custom := func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
		cfg := &ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(cfg)
		}
		capturedOpts = cfg
		return nil, nil
	}
	SetClientBuilder(custom)

	b := GetClientBuilder()
	_, err := b(
		context.Background(),
		WithAddress("localhost:19530"),
		WithUsername("user"),
		WithPassword("pass"),
		WithDBName("db"),
		WithAPIKey("key"),
	)
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	require.Equal(t, "localhost:19530", capturedOpts.Address)
	require.Equal(t, "user", capturedOpts.Username)
	require.Equal(t, "pass", capturedOpts.Password)
	require.Equal(t, "db", capturedOpts.DBName)
	require.Equal(t, "key", capturedOpts.APIKey)
}

// Test_defaultClientBuilder test default client builder
func Test_defaultClientBuilder(t *testing.T) {
	ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
	cli, err := defaultClientBuilder(ctx,
		WithAddress("invalid"),
		WithUsername("user"),
		WithPassword("pass"),
		WithDBName("db"),
		WithAPIKey("key"),
		WithDialOptions(grpc.WithInsecure()),
	)
	require.Error(t, err)
	require.Nil(t, cli)
}
