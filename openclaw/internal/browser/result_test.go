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

func TestWrapUntrustedText_Truncates(t *testing.T) {
	t.Parallel()

	got := wrapUntrustedText("abcdef", 3)
	require.Contains(t, got, untrustedBrowserWarning)
	require.Contains(t, got, "abc...")
}

func TestExtractText_UnwrapsContentEnvelope(t *testing.T) {
	t.Parallel()

	got := extractText(map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "hello"},
		},
	})
	require.Equal(t, "hello", got)
}

func TestParseTabs_ParsesActiveTab(t *testing.T) {
	t.Parallel()

	tabs := parseTabs("> 2 Example Domain - https://example.com\n")
	require.Len(t, tabs, 1)
	require.Equal(t, "tab-2", tabs[0].TargetID)
	require.Equal(t, 2, tabs[0].Index)
	require.True(t, tabs[0].Active)
	require.Equal(t, "Example Domain", tabs[0].Title)
	require.Equal(t, "https://example.com", tabs[0].URL)
}

func TestParseTargetID_SupportsPrefix(t *testing.T) {
	t.Parallel()

	got, err := parseTargetID("tab-7")
	require.NoError(t, err)
	require.Equal(t, 7, got)
}

func TestNewBaseResult_DefaultsDriverType(t *testing.T) {
	t.Parallel()

	got := newBaseResult(actionSnapshot, defaultProfileName, "", false)
	require.Equal(t, driverTypePlaywrightMCP, got.Driver)
	require.Equal(t, actionSnapshot, got.Action)
}

func TestSplitTitleURL_SupportsBareURLAndText(t *testing.T) {
	t.Parallel()

	title, url := splitTitleURL("https://example.com")
	require.Empty(t, title)
	require.Equal(t, "https://example.com", url)

	title, url = splitTitleURL("plain text")
	require.Equal(t, "plain text", title)
	require.Empty(t, url)
}

func TestParseTargetID_RejectsBadValues(t *testing.T) {
	t.Parallel()

	_, err := parseTargetID("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
}

func TestUnwrapContent_FallsBackToOriginalPayload(t *testing.T) {
	t.Parallel()

	got := unwrapContent(map[string]any{
		"message": "hello",
	})
	require.Equal(t, map[string]any{"message": "hello"}, got)
}
