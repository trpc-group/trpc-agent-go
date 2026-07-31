//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedact(t *testing.T) {
	tests := []string{
		"API_KEY=sk-1234567890abcdef1234",
		"Authorization: Bearer abcdefghijklmnop",
		"https://user:password@example.com/path",
		"https://example.com/path?token=hunter2&api_key=abcdef",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
	}
	for _, input := range tests {
		output, changed := Redact(input)
		require.True(t, changed)
		require.NotContains(t, strings.ToLower(output), "password@example")
		require.NotContains(t, output, "1234567890abcdef1234")
		require.NotContains(t, output, "abcdefghijklmnop")
		require.NotContains(t, output, "\nsecret\n")
	}
	output, changed := Redact("go test ./...")
	require.False(t, changed)
	require.Equal(t, "go test ./...", output)

	output, changed = Redact(
		"https://example.com/path?token=hunter2&api_key=abcdef",
	)
	require.True(t, changed)
	require.NotContains(t, output, "hunter2")
	require.NotContains(t, output, "abcdef")
}

func TestSanitizeReportTextTruncatesBoundedUTF8(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "ascii",
			input: strings.Repeat("a", maxReportTextBytes+100),
		},
		{
			name: "multibyte boundary",
			input: strings.Repeat("a", maxReportTextBytes-20) +
				strings.Repeat("界", 20),
		},
		{
			name: "invalid byte before boundary",
			input: string(append(
				[]byte("prefix\xff"),
				[]byte(strings.Repeat("b", maxReportTextBytes))...,
			)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, changed := sanitizeReportText(test.input)
			require.True(t, changed)
			require.LessOrEqual(t, len(output), maxReportTextBytes)
			require.True(t, strings.HasSuffix(output, "\n...[truncated]..."))
			require.Greater(t, len(output), len("\n...[truncated]..."))
		})
	}
}
