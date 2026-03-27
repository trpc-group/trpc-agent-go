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

func TestNavigationPolicy_AdditionalBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		policy navigationPolicy
		raw    string
		want   string
	}{
		{
			name: "empty url",
			raw:  "",
		},
		{
			name: "invalid url",
			raw:  "http://[::1",
			want: "invalid browser url",
		},
		{
			name: "about page",
			raw:  "about:blank",
		},
		{
			name: "unsupported scheme",
			raw:  "mailto:test@example.com",
			want: "not allowed",
		},
		{
			name: "empty host",
			raw:  "https:///docs",
		},
		{
			name: "loopback allowed",
			policy: navigationPolicy{
				AllowLoopback: true,
			},
			raw: "http://api.localhost:8080",
		},
		{
			name: "private network allowed",
			policy: navigationPolicy{
				AllowPrivateNet: true,
			},
			raw: "http://10.0.0.8",
		},
		{
			name: "blocked domain",
			policy: navigationPolicy{
				BlockedDomains: []string{"example.com"},
			},
			raw:  "https://docs.example.com",
			want: "blocked",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.policy.Validate(tc.raw)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	require.True(t, isLoopbackHost("api.localhost"))
}
