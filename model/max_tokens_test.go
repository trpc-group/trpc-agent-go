//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeMaxTokensPtr(t *testing.T) {
	t.Parallel()
	require.Nil(t, SanitizeMaxTokensPtr(nil))

	z := 0
	require.Nil(t, SanitizeMaxTokensPtr(&z))

	neg := -1
	require.Nil(t, SanitizeMaxTokensPtr(&neg))

	one := 1
	out := SanitizeMaxTokensPtr(&one)
	require.NotNil(t, out)
	require.Equal(t, 1, *out)
	require.Same(t, &one, out)

	many := 2048
	out2 := SanitizeMaxTokensPtr(&many)
	require.NotNil(t, out2)
	require.Equal(t, 2048, *out2)
	require.Same(t, &many, out2)
}

func TestClampMaxTokensForModel(t *testing.T) {
	t.Parallel()
	require.Nil(t, ClampMaxTokensForModel("gpt-4o", nil))

	over := 114687
	out := ClampMaxTokensForModel("gpt-4o", &over)
	require.NotNil(t, out)
	require.Equal(t, 16384, *out)

	within := 4096
	out2 := ClampMaxTokensForModel("gpt-4o", &within)
	require.NotNil(t, out2)
	require.Equal(t, 4096, *out2)
	require.Same(t, &within, out2)

	unknown := 50000
	out3 := ClampMaxTokensForModel("unknown-model-xyz", &unknown)
	require.NotNil(t, out3)
	require.Equal(t, 50000, *out3)
}
