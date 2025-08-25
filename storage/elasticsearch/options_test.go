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
	"time"

	"github.com/stretchr/testify/require"
)

// Test that all option setters correctly populate ClientBuilderOpts.
func TestOptions_SettersApplyValues(t *testing.T) {
	opts := &ClientBuilderOpts{}

	const (
		username = "user"
		password = "pass"
		apiKey   = "apikey"
		fp       = "fingerprint"
		prefix   = "pfx"
		vecDim   = 128
	)

	addresses := []string{"http://es1:9200", "http://es2:9200"}
	statusCodes := []int{429, 503}
	timeout := 10 * time.Second

	WithAddresses(addresses)(opts)
	WithUsername(username)(opts)
	WithPassword(password)(opts)
	WithAPIKey(apiKey)(opts)
	WithCertificateFingerprint(fp)(opts)
	WithCompressRequestBody(true)(opts)
	WithEnableMetrics(true)(opts)
	WithEnableDebugLogger(true)(opts)
	WithRetryOnStatus(statusCodes)(opts)
	WithMaxRetries(7)(opts)
	WithRetryOnTimeout(true)(opts)
	WithRequestTimeout(timeout)(opts)
	WithIndexPrefix(prefix)(opts)
	WithVectorDimension(vecDim)(opts)

	require.Equal(t, addresses, opts.Addresses)
	require.Equal(t, username, opts.Username)
	require.Equal(t, password, opts.Password)
	require.Equal(t, apiKey, opts.APIKey)
	require.Equal(t, fp, opts.CertificateFingerprint)
	require.True(t, opts.CompressRequestBody)
	require.True(t, opts.EnableMetrics)
	require.True(t, opts.EnableDebugLogger)
	require.Equal(t, statusCodes, opts.RetryOnStatus)
	require.Equal(t, 7, opts.MaxRetries)
	require.True(t, opts.RetryOnTimeout)
	require.Equal(t, timeout, opts.RequestTimeout)
	require.Equal(t, prefix, opts.IndexPrefix)
	require.Equal(t, vecDim, opts.VectorDimension)
}

// Test that WithExtraOptions accumulates and preserves order.
func TestOptions_ExtraOptionsOrderAccumulate(t *testing.T) {
	opts := &ClientBuilderOpts{}
	const (
		first  = "alpha"
		second = "beta"
		third  = "gamma"
	)
	WithExtraOptions(first)(opts)
	WithExtraOptions(second, third)(opts)

	require.Equal(t, []any{first, second, third}, opts.ExtraOptions)
}

// Test that RegisterElasticsearchInstance appends options, not overwrites.
func TestOptions_RegistryAppendBehavior(t *testing.T) {
	// Isolate global state.
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	const name = "test-append"
	RegisterElasticsearchInstance(name,
		WithAddresses([]string{"http://a:9200"}),
		WithUsername("u1"),
	)
	RegisterElasticsearchInstance(name,
		WithPassword("p2"),
		WithIndexPrefix("px"),
		WithExtraOptions("x"),
	)

	opts, ok := GetElasticsearchInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 5)

	applied := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(applied)
	}
	require.Equal(t, []string{"http://a:9200"}, applied.Addresses)
	require.Equal(t, "u1", applied.Username)
	require.Equal(t, "p2", applied.Password)
	require.Equal(t, "px", applied.IndexPrefix)
	require.Equal(t, []any{"x"}, applied.ExtraOptions)
}
