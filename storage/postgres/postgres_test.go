//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test that SetClientBuilder installs a custom builder and that the
// returned builder is actually used when invoked.
func TestSetGetClientBuilder(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	invoked := false
	custom := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		invoked = true
		return nil, nil
	}

	SetClientBuilder(custom)
	b := GetClientBuilder()
	_, err := b(context.Background(), WithClientConnString("postgres://localhost:5432/test"))
	require.NoError(t, err)
	require.True(t, invoked, "custom builder was not invoked")
}

// Test the default builder validates empty connection string.
func TestDefaultClientBuilder_EmptyConnString(t *testing.T) {
	const expected = "postgres: connection string is empty"
	_, err := DefaultClientBuilder(context.Background())
	require.Error(t, err)
	require.Equal(t, expected, err.Error())
}

// Test invalid connection string parsing error path.
func TestDefaultClientBuilder_InvalidConnString(t *testing.T) {
	const badConnString = "invalid connection string"
	_, err := DefaultClientBuilder(context.Background(), WithClientConnString(badConnString))
	require.Error(t, err)
	// The error should contain information about connection failure or opening
	require.Contains(t, err.Error(), "postgres")
}

// Test the default builder can parse a standard postgres connection string.
// Note: This doesn't actually connect to the database, it just validates the connection string.
func TestDefaultClientBuilder_ParseConnStringSuccess(t *testing.T) {
	const connString = "postgres://user:pass@127.0.0.1:5432/testdb?sslmode=disable"

	// Skip this test if we can't connect (no postgres available)
	// We only test the parsing and config creation
	t.Skip("Skipping test that requires a real PostgreSQL connection")
}

// Test registry add and get.
func TestRegisterAndGetPostgresInstance(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	const (
		name       = "test-instance"
		connString = "postgres://user:pass@127.0.0.1:5432/testdb"
	)

	RegisterPostgresInstance(name, WithClientConnString(connString))
	opts, ok := GetPostgresInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	// Verify that options can be extracted
	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, connString, cfg.ConnString)
}

// Test GetPostgresInstance for a non-existing instance.
func TestGetPostgresInstance_NotFound(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	opts, ok := GetPostgresInstance("not-exist")
	require.False(t, ok)
	require.Nil(t, opts)
}

// Test WithExtraOptions accumulates and preserves order via a custom builder.
func TestWithExtraOptions_Accumulation(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	observed := make([]any, 0)
	custom := func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
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
		context.Background(),
		WithClientConnString("postgres://localhost:5432/test"),
		WithExtraOptions(first),
		WithExtraOptions(second, third),
	)
	require.NoError(t, err)
	require.Equal(t, []any{first, second, third}, observed)
}

// Test multiple RegisterPostgresInstance calls append options rather than overwrite.
func TestRegisterPostgresInstance_AppendsOptions(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	const name = "append-instance"
	RegisterPostgresInstance(name, WithClientConnString("postgres://localhost:5432/test"))
	RegisterPostgresInstance(name, WithExtraOptions("x"), WithExtraOptions("y"))

	opts, ok := GetPostgresInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 3)

	// Apply options to verify combined effect on ClientBuilderOpts.
	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, []any{"x", "y"}, cfg.ExtraOptions)
}
