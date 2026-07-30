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

func TestCompactBrowserErrorText_CompactsCrashLog(t *testing.T) {
	t.Parallel()

	got, ok := compactBrowserErrorText(browserCrashPayloadText())
	require.True(t, ok)
	require.Contains(t, got, browserCrashSummary)
	require.Contains(t, got, "Target page, context or browser")
	require.NotContains(t, got, "--disable-field-trial-config")
	require.NotContains(t, got, browserLaunchMarker)
	require.Less(t, len(got), 600)
}

func TestCompactBrowserErrorText_IgnoresPlainPageText(t *testing.T) {
	t.Parallel()

	got, ok := compactBrowserErrorText(
		"Article: SIGTRAP is a signal. A process did exit yesterday.",
	)
	require.False(t, ok)
	require.Empty(t, got)
}

func TestBlockedBrowserPageReason_DetectsCommonChallenges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "cloudflare title",
			text: "Page Title: Just a moment...\n" +
				"Checking if the site connection is secure",
			want: "Cloudflare",
		},
		{
			name: "short just a moment body",
			text: "Just a moment......",
			want: "Cloudflare",
		},
		{
			name: "unusual traffic",
			text: "Our systems have detected unusual traffic from " +
				"your computer network.",
			want: "unusual-traffic",
		},
		{
			name: "captcha",
			text: "Please complete the CAPTCHA to verify you are human.",
			want: "CAPTCHA",
		},
		{
			name: "human verification",
			text: "Checking if the site connection is secure. " +
				"Enable JavaScript and cookies to continue.",
			want: "human-verification",
		},
		{
			name: "bot check",
			text: "This page is running an anti-bot check. " +
				"Please wait while we verify your browser.",
			want: "bot-check",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := blockedBrowserPageReason(tc.text)
			require.True(t, ok)
			require.Contains(t, got, tc.want)
		})
	}
}

func TestBlockedBrowserPageReason_IgnoresPlainPageText(t *testing.T) {
	t.Parallel()

	cases := []string{
		"This article says a captcha can be hard to read.",
		"This security article compares anti-bot systems and bot check terminology.",
		"The phrase just a moment appeared in the transcript.",
		"Just a moment in the article title, then regular text.",
		"Verify your account settings before changing the profile.",
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc, func(t *testing.T) {
			t.Parallel()

			got, ok := blockedBrowserPageReason(tc)
			require.False(t, ok)
			require.Empty(t, got)
		})
	}
}

func TestBrowserResultURL(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://example.com/final", browserResultURL(map[string]any{
		"url": "https://example.com/final",
	}))
	require.Equal(t, "https://example.com/mcp", browserResultURL(textPayload(
		"Page URL: https://example.com/mcp",
	)))
	require.Empty(t, browserResultURL(nil))
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
	require.NotContains(t, got.Supported, actionEvaluate)
}

func TestNewBaseResult_ShowsEvaluateWhenEnabled(t *testing.T) {
	t.Parallel()

	got := newBaseResult(actionSnapshot, defaultProfileName, "", true)
	require.Contains(t, got.Supported, actionEvaluate)
}

func TestSupportedActionsForDriver_HidesServerOnlyActions(t *testing.T) {
	t.Parallel()

	mcpActions := supportedActionsForDriver(driverTypePlaywrightMCP)
	require.Contains(t, mcpActions, actionNavigate)
	require.Contains(t, mcpActions, actionAct)
	require.Contains(t, mcpActions, actionEvaluate)
	require.NotContains(t, mcpActions, actionPDF)
	require.NotContains(t, mcpActions, actionCookies)
	require.NotContains(t, mcpActions, actionStorage)
	require.NotContains(t, mcpActions, actionDownload)

	serverActions := supportedActionsForDriver(driverTypeBrowserServer)
	require.Contains(t, serverActions, actionPDF)
	require.Contains(t, serverActions, actionCookies)
	require.Contains(t, serverActions, actionStorage)
	require.Contains(t, serverActions, actionDownload)
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
