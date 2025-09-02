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

// Test that WithExtraOptions accumulates and preserves order.
func TestOptions_ExtraOptionsOrderAccumulate(t *testing.T) {
	opts := &ClientBuilderOpts{}
	first := &Config{Addresses: []string{"http://es1:9200"}}
	second := "beta"
	third := 123
	WithExtraOptions(first)(opts)
	WithExtraOptions(second, third)(opts)

	require.Equal(t, []any{first, second, third}, opts.ExtraOptions)
}

func TestOptions_WithVersion_SetsField(t *testing.T) {
	opts := &ClientBuilderOpts{}
	WithVersion(ESVersionV7)(opts)
	require.Equal(t, ESVersionV7, opts.Version)
}

// Test that RegisterElasticsearchInstance appends options, not overwrites.
func TestOptions_RegistryAppendBehavior(t *testing.T) {
	// Isolate global state.
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	const name = "test-append"
	RegisterElasticsearchInstance(name,
		WithExtraOptions(&Config{Addresses: []string{"http://a:9200"}}),
		WithVersion(ESVersionV8),
	)
	RegisterElasticsearchInstance(name,
		WithExtraOptions("x"),
	)

	opts, ok := GetElasticsearchInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 2)

	applied := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(applied)
	}
	require.Len(t, applied.ExtraOptions, 2)
	require.Equal(t, ESVersionV8, applied.Version)
}
