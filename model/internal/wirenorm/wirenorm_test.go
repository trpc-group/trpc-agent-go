//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package wirenorm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNeedsPlaceholder(t *testing.T) {
	require.Equal(t, " ", EmptyContentPlaceholder)
	require.False(t, NeedsPlaceholder("text", false, true, true, true))
	require.False(t, NeedsPlaceholder("", true, true, true, true))
	require.False(t, NeedsPlaceholder("", false, false, false, false))
	require.True(t, NeedsPlaceholder("", false, true, false, false))
	require.True(t, NeedsPlaceholder("", false, false, true, false))
	require.True(t, NeedsPlaceholder("", false, false, false, true))
}
