//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides the redis instance info management.
package redis

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Test that SetClientBuilder installs a custom builder and that the
// returned builder is actually used when invoked.
func TestSetGetClientBuilder(t *testing.T) {
	// Isolate global state.
	oldRegistry := redisRegistry
	redisRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { redisRegistry = oldRegistry }()

	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	invoked := false
	custom := func(opts ...ClientBuilderOpt) (redis.UniversalClient, error) {
		invoked = true
		return nil, nil
	}

	SetClientBuilder(custom)
	b := GetClientBuilder()
	_, err := b(WithClientBuilderURL("redis://localhost:6379"))
	require.NoError(t, err)
	require.True(t, invoked, "custom builder was not invoked")
}

// Test the default builder validates empty URL.
func TestDefaultClientBuilder_EmptyURL(t *testing.T) {
	const expected = "redis: url is empty"
	_, err := defaultClientBuilder()
	require.Error(t, err)
	require.Equal(t, expected, err.Error())
}

// Test invalid URL parsing error path.
func TestDefaultClientBuilder_InvalidURL(t *testing.T) {
	const (
		badURL = "127.0.0.1:6379"
		prefix = "redis: parse url 127.0.0.1:6379:"
	)
	_, err := defaultClientBuilder(WithClientBuilderURL(badURL))
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), prefix))
}

// Test the default builder can parse a standard redis URL and returns a
// non-nil client without connecting.
func TestDefaultClientBuilder_ParseURLSuccess(t *testing.T) {
	const urlStr = "redis://user:pass@127.0.0.1:6379/0"
	client, err := defaultClientBuilder(
		WithClientBuilderURL(urlStr),
	)
	require.NoError(t, err)
	require.NotNil(t, client)
}

// Test registry add and get.
func TestRegisterAndGetRedisInstance(t *testing.T) {
	// Isolate global state.
	oldRegistry := redisRegistry
	redisRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { redisRegistry = oldRegistry }()

	const (
		name   = "test-instance"
		urlStr = "redis://127.0.0.1:6379/1"
	)

	RegisterRedisInstance(name, WithClientBuilderURL(urlStr))
	opts, ok := GetRedisInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	// Build a client using the stored options to ensure they are usable.
	client, err := defaultClientBuilder(opts...)
	require.NoError(t, err, "unexpected error building client with stored opts")
	require.NotNil(t, client, "expected non-nil client from stored options")
}

// Test GetRedisInstance for a non-existing instance.
func TestGetRedisInstance_NotFound(t *testing.T) {
	// Isolate global state.
	oldRegistry := redisRegistry
	redisRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { redisRegistry = oldRegistry }()

	opts, ok := GetRedisInstance("not-exist")
	require.False(t, ok)
	require.Nil(t, opts)
}

// Test WithExtraOptions accumulates and preserves order via a custom builder.
func TestWithExtraOptions_Accumulation(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	observed := make([]any, 0)
	custom := func(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error) {
		cfg := &ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(cfg)
		}
		observed = append(observed, cfg.ExtraOptions...)
		return nil, nil
	}
	SetClientBuilder(custom)

	const (
		first  = "alpha"
		second = "beta"
		third  = "gamma"
	)
	b := GetClientBuilder()
	_, err := b(
		WithClientBuilderURL("redis://localhost:6379"),
		WithExtraOptions(first),
		WithExtraOptions(second, third),
	)
	require.NoError(t, err)
	require.Equal(t, []any{first, second, third}, observed)
}

// Test multiple RegisterRedisInstance calls append options rather than overwrite.
func TestRegisterRedisInstance_AppendsOptions(t *testing.T) {
	// Isolate global state.
	oldRegistry := redisRegistry
	redisRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { redisRegistry = oldRegistry }()

	const name = "append-instance"
	RegisterRedisInstance(name, WithClientBuilderURL("redis://localhost:6379"))
	RegisterRedisInstance(name, WithExtraOptions("x"), WithExtraOptions("y"))

	opts, ok := GetRedisInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 3)

	// Apply options to verify combined effect on ClientBuilderOpts.
	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, []any{"x", "y"}, cfg.ExtraOptions)
}
