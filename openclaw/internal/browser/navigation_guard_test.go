//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNavigationPolicy_BlocksLoopbackByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("http://127.0.0.1:8080")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked")
}

func TestNavigationPolicy_AllowlistBlocksOtherDomains(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedDomains: []string{"example.com"},
	}.Validate("https://google.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not allowed")
}

func TestNavigationPolicy_AllowsSubdomain(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedDomains: []string{"example.com"},
	}.Validate("https://docs.example.com")
	require.NoError(t, err)
}

func TestNavigationPolicy_BlocksFileURLByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("file:///tmp/test.html")
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNavigationPolicy_BlocksPrivateNetworkByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("http://192.168.1.20")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private network")
}

func TestNavigationPolicy_AllowsFileURLWhenEnabled(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowFileURLs: true,
	}.Validate("file:///tmp/test.html")
	require.NoError(t, err)
}

func TestNormalizeDomains_DedupesAndTrims(t *testing.T) {
	t.Parallel()

	got := normalizeDomains([]string{
		" Example.com ",
		"example.com.",
		"",
	})
	require.Equal(t, []string{"example.com"}, got)
	require.True(t, hostMatchesDomain("docs.example.com", "example.com"))
	require.False(t, hostMatchesDomain("google.com", "example.com"))
}
