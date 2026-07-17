//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreviousSummaryContext(t *testing.T) {
	_, ok := PreviousSummaryFromContext(nil)
	require.False(t, ok)

	_, ok = PreviousSummaryFromContext(context.Background())
	require.False(t, ok)

	ctx := ContextWithPreviousSummary(nil, "previous")
	previous, ok := PreviousSummaryFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "previous", previous)

	emptyCtx := ContextWithPreviousSummary(context.Background(), "")
	previous, ok = PreviousSummaryFromContext(emptyCtx)
	require.True(t, ok)
	require.Empty(t, previous)
}
