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
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxTokensCompatibilityHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, SanitizeMaxTokensPtr(nil))

	z := 0
	require.Nil(t, SanitizeMaxTokensPtr(&z))

	one := 1
	out := SanitizeMaxTokensPtr(&one)
	require.NotNil(t, out)
	require.Same(t, &one, out)

	over := 114687
	capped := ClampMaxTokensForModel("gpt-4o", &over)
	require.NotNil(t, capped)
	require.Equal(t, 16384, *capped)

	require.Equal(t, int32(4096), MaxTokensToInt32(4096))
	require.Equal(t, int32(math.MaxInt32), MaxTokensToInt32(math.MaxInt32+1))
	require.Equal(t, int32(math.MinInt32), MaxTokensToInt32(math.MinInt32-1))
}
