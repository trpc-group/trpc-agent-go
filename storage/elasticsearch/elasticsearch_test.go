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

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
	"github.com/stretchr/testify/require"
)

func TestDefaultClientBuilder_VersionSelection(t *testing.T) {
	// Default (unspecified) -> v9
	c, err := defaultClientBuilder(
		WithVersion(ESVersionUnspecified),
	)
	require.NoError(t, err)
	_, ok := c.(*esv9.Client)
	require.True(t, ok)

	// Explicit v9
	c, err = defaultClientBuilder(WithVersion(ESVersionV9))
	require.NoError(t, err)
	_, ok = c.(*esv9.Client)
	require.True(t, ok)

	// v8
	c, err = defaultClientBuilder(WithVersion(ESVersionV8))
	require.NoError(t, err)
	_, ok = c.(*esv8.Client)
	require.True(t, ok)

	// v7
	c, err = defaultClientBuilder(WithVersion(ESVersionV7))
	require.NoError(t, err)
	_, ok = c.(*esv7.Client)
	require.True(t, ok)

	// unknown
	_, err = defaultClientBuilder(WithVersion(ESVersion("unknown")))
	require.Error(t, err)
	require.Equal(t, "elasticsearch: unknown version unknown", err.Error())
}
