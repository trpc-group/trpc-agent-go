//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package aggregator

import (
	"context"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
)

func TestAggregatorMergesSameMessage(t *testing.T) {
	ctx := context.Background()
	agg := New(ctx)

	events, err := agg.Append(ctx, aguievents.NewTextMessageContentEvent("msg", "hello"))
	require.NoError(t, err)
	require.Nil(t, events)

	events, err = agg.Append(ctx, aguievents.NewTextMessageContentEvent("msg", "world"))
	require.NoError(t, err)
	require.Nil(t, events)

	flushed, err := agg.Flush(ctx)
	require.NoError(t, err)
	require.Len(t, flushed, 1)
	content, ok := flushed[0].(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "helloworld", content.Delta)

	flushed, err = agg.Flush(ctx)
	require.NoError(t, err)
	require.Nil(t, flushed)
}

func TestAggregatorFlushOnMessageChange(t *testing.T) {
	ctx := context.Background()
	agg := New(ctx)

	firstFlush, err := agg.Append(ctx, aguievents.NewTextMessageContentEvent("first", "hi"))
	require.NoError(t, err)
	require.Nil(t, firstFlush)

	flushed, err := agg.Append(ctx, aguievents.NewTextMessageContentEvent("second", "there"))
	require.NoError(t, err)
	require.Len(t, flushed, 1)
	first, ok := flushed[0].(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "hi", first.Delta)

	rest, err := agg.Flush(ctx)
	require.NoError(t, err)
	require.Len(t, rest, 1)
	second, ok := rest[0].(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "there", second.Delta)
}

func TestAggregatorFlushesBeforeNonTextEvent(t *testing.T) {
	ctx := context.Background()
	agg := New(ctx)

	_, err := agg.Append(ctx, aguievents.NewTextMessageContentEvent("msg", "abc"))
	require.NoError(t, err)

	runStarted := aguievents.NewRunStartedEvent("thread", "run")
	events, err := agg.Append(ctx, runStarted)
	require.NoError(t, err)
	require.Len(t, events, 2)

	content, ok := events[0].(*aguievents.TextMessageContentEvent)
	require.True(t, ok)
	require.Equal(t, "abc", content.Delta)
	require.Same(t, runStarted, events[1])
}

func TestAggregatorDisabledPassThrough(t *testing.T) {
	ctx := context.Background()
	content := aguievents.NewTextMessageContentEvent("msg", "data")
	agg := New(ctx, WithEnabled(false))

	events, err := agg.Append(ctx, content)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Same(t, content, events[0])

	flushed, err := agg.Flush(ctx)
	require.NoError(t, err)
	require.Nil(t, flushed)
}
