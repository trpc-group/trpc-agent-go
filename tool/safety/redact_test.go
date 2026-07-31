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
}
