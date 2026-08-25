//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summarydiag

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestFormatFilterKey(t *testing.T) {
	markerRunes := utf8.RuneCountInString(truncatedMarker)
	prefixLimit := filterKeyPrefixLimit()
	require.GreaterOrEqual(t, prefixLimit, 0)
	require.Equal(t, FilterKeyMaxRunes, prefixLimit+markerRunes,
		"prefix plus marker must fill the display budget without a magic number")

	longASCII := strings.Repeat("a", FilterKeyMaxRunes+1)
	longCJK := strings.Repeat("摘", FilterKeyMaxRunes+1)
	longEmoji := strings.Repeat("😀", FilterKeyMaxRunes+1)
	exactASCII := strings.Repeat("b", FilterKeyMaxRunes)
	exactCJK := strings.Repeat("要", FilterKeyMaxRunes)

	tests := []struct {
		name          string
		key           string
		wantDisplay   string
		wantTruncated bool
		wantUnchanged bool
	}{
		{
			name:          "empty stays empty",
			key:           "",
			wantDisplay:   "",
			wantTruncated: false,
			wantUnchanged: true,
		},
		{
			name:          "short ascii is unchanged",
			key:           "branch/user",
			wantDisplay:   "branch/user",
			wantTruncated: false,
			wantUnchanged: true,
		},
		{
			name:          "exact ascii limit is unchanged",
			key:           exactASCII,
			wantDisplay:   exactASCII,
			wantTruncated: false,
			wantUnchanged: true,
		},
		{
			name:          "oversize ascii is truncated",
			key:           longASCII,
			wantDisplay:   strings.Repeat("a", prefixLimit) + truncatedMarker,
			wantTruncated: true,
		},
		{
			name:          "short multibyte utf8 is unchanged",
			key:           "应用/用户",
			wantDisplay:   "应用/用户",
			wantTruncated: false,
			wantUnchanged: true,
		},
		{
			name:          "exact multibyte limit is unchanged",
			key:           exactCJK,
			wantDisplay:   exactCJK,
			wantTruncated: false,
			wantUnchanged: true,
		},
		{
			name:          "oversize multibyte utf8 is truncated on a rune boundary",
			key:           longCJK,
			wantDisplay:   strings.Repeat("摘", prefixLimit) + truncatedMarker,
			wantTruncated: true,
		},
		{
			name:          "oversize emoji is truncated on a rune boundary",
			key:           longEmoji,
			wantDisplay:   strings.Repeat("😀", prefixLimit) + truncatedMarker,
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			display, truncated := FormatFilterKey(tt.key)
			require.Equal(t, tt.wantTruncated, truncated)
			require.Equal(t, tt.wantDisplay, display)
			require.True(t, utf8.ValidString(display))
			require.LessOrEqual(t, utf8.RuneCountInString(display), FilterKeyMaxRunes)
			if tt.wantUnchanged {
				require.Equal(t, tt.key, display)
				return
			}
			require.Equal(t, FilterKeyMaxRunes, utf8.RuneCountInString(display),
				"truncated display must consume the full budget including the marker")
			require.True(t, strings.HasSuffix(display, truncatedMarker))
			prefix := strings.TrimSuffix(display, truncatedMarker)
			require.Equal(t, prefixLimit, utf8.RuneCountInString(prefix))
			require.True(t, strings.HasPrefix(tt.key, prefix),
				"the displayed prefix must be the original key's leading runes")
		})
	}
}

func TestFormatFilterKeyDoesNotSplitMultibyteRune(t *testing.T) {
	prefixLimit := filterKeyPrefixLimit()
	// The last kept rune is a 4-byte emoji; the cut must keep it intact.
	key := strings.Repeat("x", prefixLimit-1) + "😀" + strings.Repeat("y", FilterKeyMaxRunes)
	display, truncated := FormatFilterKey(key)
	require.True(t, truncated)
	require.Equal(t,
		strings.Repeat("x", prefixLimit-1)+"😀"+truncatedMarker,
		display)
	require.True(t, utf8.ValidString(display))
	require.Equal(t, FilterKeyMaxRunes, utf8.RuneCountInString(display))
	require.NotContains(t, display, "\xff")
}
