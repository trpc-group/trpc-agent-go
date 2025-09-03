//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptions_Setters_Table(t *testing.T) {
	t.Run("basic setters", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithAddresses([]string{"http://a:9200", "http://b:9200"})(opts)
		WithUsername("user")(opts)
		WithPassword("pass")(opts)
		WithAPIKey("apikey")(opts)
		WithCertificateFingerprint("fp")(opts)
		WithCompressRequestBody(true)(opts)
		WithEnableMetrics(true)(opts)
		WithEnableDebugLogger(true)(opts)
		WithRetryOnStatus([]int{500, 429})(opts)
		WithMaxRetries(5)(opts)
		WithVersion(ESVersionV8)(opts)

		require.Equal(t, []string{"http://a:9200", "http://b:9200"}, opts.Addresses)
		require.Equal(t, "user", opts.Username)
		require.Equal(t, "pass", opts.Password)
		require.Equal(t, "apikey", opts.APIKey)
		require.Equal(t, "fp", opts.CertificateFingerprint)
		require.True(t, opts.CompressRequestBody)
		require.True(t, opts.EnableMetrics)
		require.True(t, opts.EnableDebugLogger)
		require.Equal(t, []int{500, 429}, opts.RetryOnStatus)
		require.Equal(t, 5, opts.MaxRetries)
		require.Equal(t, ESVersionV8, opts.Version)
	})

	t.Run("extra options accumulate in order", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		first := map[string]any{"k": 1}
		second := "x"
		third := 3

		WithExtraOptions(first)(opts)
		WithExtraOptions(second, third)(opts)

		require.Equal(t, []any{first, second, third}, opts.ExtraOptions)
	})
}

func TestOptions_Registry_AppendAndGet(t *testing.T) {
	// Isolate global state.
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	const name = "cluster-a"
	RegisterElasticsearchInstance(name,
		WithAddresses([]string{"http://a:9200"}),
		WithUsername("u1"),
	)
	RegisterElasticsearchInstance(name,
		WithRetryOnStatus([]int{500}),
	)

	opts, ok := GetElasticsearchInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 3)

	applied := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(applied)
	}
	// Validate all applied fields.
	require.Equal(t, []string{"http://a:9200"}, applied.Addresses)
	require.Equal(t, "u1", applied.Username)
	require.Equal(t, []int{500}, applied.RetryOnStatus)
}

func TestOptions_Registry_NotFound(t *testing.T) {
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	opts, ok := GetElasticsearchInstance("missing")
	require.False(t, ok)
	require.Nil(t, opts)
}

func TestGlobalBuilder_SetAndGet(t *testing.T) {
	// Isolate global state.
	old := globalBuilder
	defer func() { globalBuilder = old }()

	// Test initial state - should be DefaultClientBuilder.
	builder := GetClientBuilder()
	require.NotNil(t, builder)

	// Test setting custom builder.
	customBuilder := func(opts ...ClientBuilderOpt) (Client, error) {
		return &clientV9{}, nil
	}
	SetClientBuilder(customBuilder)

	// Test getting the set builder.
	retrieved := GetClientBuilder()
	require.NotNil(t, retrieved)

	// Test building with custom builder.
	client, err := retrieved(WithVersion(ESVersionV9))
	require.NoError(t, err)
	require.NotNil(t, client)
}
