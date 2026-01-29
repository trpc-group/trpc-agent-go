//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsURLStart_ReturnsExpected verifies URL prefix detection matches supported schemes.
func TestIsURLStart_ReturnsExpected(t *testing.T) {
	require.False(t, isURLStart("http:"))
	require.False(t, isURLStart("unknown://"))
	require.True(t, isURLStart("http://"))
}

// TestRemoveAtIndex_ReturnsExpected verifies rune removal handles out-of-range inputs safely.
func TestRemoveAtIndex_ReturnsExpected(t *testing.T) {
	require.Equal(t, []rune("abc"), removeAtIndex([]rune("abc"), -1, 1))
	require.Equal(t, []rune("abc"), removeAtIndex([]rune("abc"), 1, -1))
	require.Equal(t, []rune("abc"), removeAtIndex([]rune("abc"), 2, 5))
	require.Equal(t, []rune("ac"), removeAtIndex([]rune("abc"), 1, 1))
}

// TestEndsWithCommaOrNewline_ReturnsExpected verifies trailing comma and newline detection.
func TestEndsWithCommaOrNewline_ReturnsExpected(t *testing.T) {
	require.False(t, endsWithCommaOrNewline(nil))
	require.True(t, endsWithCommaOrNewline([]rune("a,\t")))
	require.True(t, endsWithCommaOrNewline([]rune("a\n")))
	require.False(t, endsWithCommaOrNewline([]rune("a ")))
}
