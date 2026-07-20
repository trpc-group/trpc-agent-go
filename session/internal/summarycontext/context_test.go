//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreviousSummary(t *testing.T) {
	_, ok := PreviousSummary(nil)
	require.False(t, ok)

	_, ok = PreviousSummary(context.Background())
	require.False(t, ok)

	ctx := WithPreviousSummary(nil, "previous")
	previous, ok := PreviousSummary(ctx)
	require.True(t, ok)
	require.Equal(t, "previous", previous)

	emptyCtx := WithPreviousSummary(context.Background(), "")
	previous, ok = PreviousSummary(emptyCtx)
	require.True(t, ok)
	require.Empty(t, previous)
}
