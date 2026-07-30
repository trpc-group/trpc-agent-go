//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package searchresult

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatch_SearchResultPages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		name string
	}{
		{
			raw:  "https://www.google.com/search?q=openclaw",
			name: "Google search",
		},
		{
			raw:  "https://scholar.google.com/scholar?q=openclaw",
			name: "Google Scholar search",
		},
		{
			raw:  "https://webcache.googleusercontent.com/search?q=x",
			name: "Google cached search",
		},
		{
			raw:  "https://duckduckgo.com/?q=openclaw",
			name: "DuckDuckGo search",
		},
		{
			raw:  "https://html.duckduckgo.com/html/?q=openclaw",
			name: "DuckDuckGo HTML search",
		},
		{
			raw:  "https://lite.duckduckgo.com/lite/?q=openclaw",
			name: "DuckDuckGo Lite search",
		},
		{
			raw:  "https://www.bing.com/search?q=openclaw",
			name: "Bing search",
		},
		{
			raw:  "https://search.brave.com/search?q=openclaw",
			name: "Brave Search",
		},
		{
			raw:  "https://search.yahoo.com/search?p=openclaw",
			name: "Yahoo search",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Match(tc.raw)
			require.True(t, ok)
			require.Equal(t, tc.name, got)
		})
	}
}

func TestMatch_DoesNotMatchSourcePages(t *testing.T) {
	t.Parallel()

	cases := []string{
		"https://github.com/trpc-group/trpc-agent-go",
		"https://www.google.com/",
		"https://www.google.com/search/about",
		"https://webcache.googleusercontent.com/search/about",
		"https://scholar.google.com/citations?user=abc",
		"https://www.bing.com/maps?q=openclaw",
		"https://www.bing.com/search/overview",
		"https://duckduckgo.com/",
		"https://search.brave.com/search/help",
		"https://search.yahoo.com/search/help",
	}

	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			_, ok := Match(raw)
			require.False(t, ok)
		})
	}
}
