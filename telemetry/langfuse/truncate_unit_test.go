//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSetObservationMaxBytes_NilDisablesTruncation(t *testing.T) {
	setObservationMaxBytes(nil)
	require.Less(t, getObservationMaxBytes(), 0)

	in := strings.Repeat("x", 1024)
	out := truncateObservationValue(in)
	require.Equal(t, in, out)
}

func TestSetObservationMaxBytes_NegativeDisablesTruncation(t *testing.T) {
	v := -123
	setObservationMaxBytes(&v)
	require.Less(t, getObservationMaxBytes(), 0)

	in := strings.Repeat("x", 1024)
	out := truncateObservationValue(in)
	require.Equal(t, in, out)
}

func TestSetObservationMaxBytes_ZeroTruncatesEverything(t *testing.T) {
	v := 0
	setObservationMaxBytes(&v)
	require.Equal(t, 0, getObservationMaxBytes())

	out := truncateObservationValue("abc")
	require.Equal(t, "", out)
}

func TestTruncateStringBytes_MarkerLongerThanMax(t *testing.T) {
	// defaultTruncateMarker starts with a multi-byte rune, so very small maxBytes
	// should result in an empty prefix, but still be valid UTF-8.
	out1 := truncateStringBytes("hello", 1)
	require.True(t, utf8.ValidString(out1))
	require.LessOrEqual(t, len([]byte(out1)), 1)

	out3 := truncateStringBytes("hello", 3)
	require.True(t, utf8.ValidString(out3))
	require.Equal(t, "…", out3)
	require.Equal(t, 3, len([]byte(out3)))
}

func TestSafeUTF8PrefixSuffix_DoNotSplitRune(t *testing.T) {
	b := []byte("中a中")
	// "中" is 3 bytes. A prefix of 4 bytes must not include a partial rune.
	p := safeUTF8Prefix(b, 4)
	require.True(t, utf8.Valid(p))
	require.Equal(t, "中a", string(p))

	// A suffix of 4 bytes must also be aligned.
	s := safeUTF8Suffix(b, 4)
	require.True(t, utf8.Valid(s))
	require.Equal(t, "a中", string(s))
}
