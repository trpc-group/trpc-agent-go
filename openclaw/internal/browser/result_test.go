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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapUntrustedText_Truncates(t *testing.T) {
	t.Parallel()

	got := wrapUntrustedText("abcdef", 3)
	require.Contains(t, got, untrustedBrowserWarning)
	require.Contains(t, got, "abc...")
}

func TestWrapUntrustedText_EmptyUsesWarning(t *testing.T) {
	t.Parallel()

	require.Equal(t, untrustedBrowserWarning, wrapUntrustedText(" ", 8))
}

func TestTruncateString_SupportsRunes(t *testing.T) {
	t.Parallel()

	require.Equal(t, "你好...", truncateString("你好世界", 2))
	require.Equal(t, "abc", truncateString("abc", 0))
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

func TestExtractText_SkipsNonTextAndBadPayload(t *testing.T) {
	t.Parallel()

	got := extractText(map[string]any{
		"content": []map[string]any{
			{"type": "image", "text": "ignored"},
			{"type": "text", "text": " hello "},
			{"type": "text", "text": " "},
		},
	})
	require.Equal(t, "hello", got)

	require.Empty(t, extractText(func() {}))
	require.Empty(t, extractText(map[string]any{
		"content": map[string]any{"type": "text"},
	}))
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

func TestParseTabs_HandlesEmptyAndBareDetail(t *testing.T) {
	t.Parallel()

	require.Nil(t, parseTabs(" \n"))

	tab, ok := parseTabLine("  3 Example Domain")
	require.True(t, ok)
	require.Equal(t, "tab-3", tab.TargetID)
	require.Equal(t, "Example Domain", tab.Title)
	require.Empty(t, tab.URL)

	tab, ok = parseTabLine("> 4")
	require.True(t, ok)
	require.Equal(t, "tab-4", tab.TargetID)
	require.Empty(t, tab.Title)

	_, ok = parseTabLine("> x Broken")
	require.False(t, ok)
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

	title, url = splitTitleURL("Example | https://example.com")
	require.Equal(t, "Example", title)
	require.Equal(t, "https://example.com", url)
}

func TestParseTargetID_RejectsBadValues(t *testing.T) {
	t.Parallel()

	_, err := parseTargetID("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")

	_, err = parseTargetID(" ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestUnwrapContent_FallsBackToOriginalPayload(t *testing.T) {
	t.Parallel()

	got := unwrapContent(map[string]any{
		"message": "hello",
	})
	require.Equal(t, map[string]any{"message": "hello"}, got)

	require.Nil(t, unwrapContent(nil))
	require.Equal(t, "text", unwrapContent("text"))
	raw := func() {}
	got = unwrapContent(raw)
	require.NotNil(t, got)
	_, ok := got.(func())
	require.True(t, ok)

	type invalidEnvelope struct {
		Content json.RawMessage `json:"content"`
	}
	got = unwrapContent(invalidEnvelope{
		Content: json.RawMessage("{"),
	})
	_, ok = got.(invalidEnvelope)
	require.True(t, ok)
}
