//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outputlimit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectorBoundsCombinedStreamsWithoutShortWrites(t *testing.T) {
	collector := New(5)

	n, err := collector.Writer(Stdout).Write([]byte("abc"))
	require.NoError(t, err)
	require.Equal(t, 3, n)
	n, err = collector.Writer(Stderr).Write([]byte("def"))
	require.NoError(t, err)
	require.Equal(t, 3, n)

	stdout, stderr := collector.Strings()
	require.Equal(t, "abc", stdout)
	require.Equal(t, "de", stderr)
	require.True(t, collector.Truncated())
}

func TestCollectorWithoutLimitPreservesOutput(t *testing.T) {
	collector := New(0)
	collector.Append(Stdout, []byte("stdout"))
	collector.Append(Stderr, []byte("stderr"))

	stdout, stderr := collector.Strings()
	require.Equal(t, "stdout", stdout)
	require.Equal(t, "stderr", stderr)
	require.False(t, collector.Truncated())
}

func TestTruncateStringBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		max           int
		want          string
		wantTruncated bool
	}{
		{name: "disabled", value: "abc", max: 0, want: "abc"},
		{name: "exact", value: "abc", max: 3, want: "abc"},
		{name: "one byte over", value: "abcd", max: 3, want: "abc", wantTruncated: true},
		{name: "multibyte boundary", value: "éé", max: 3, want: "é", wantTruncated: true},
		{name: "invalid UTF-8 within limit", value: string([]byte{'a', 0xff}), max: 2, want: string([]byte{'a', 0xff})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := TruncateString(tc.value, tc.max)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantTruncated, truncated)
		})
	}
}
