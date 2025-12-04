//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import (
	"testing"
	"time"

	"context"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
)

func TestOptionsWithAggregatorFactoryAndFlushInterval(t *testing.T) {
	factoryCalled := false
	customFactory := func(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
		factoryCalled = true
		return aggregator.New(ctx, opt...)
	}

	opts := newOptions(
		WithAggregatorFactory(customFactory),
		WithAggregationOption(aggregator.WithEnabled(false)),
		WithFlushInterval(250*time.Millisecond),
	)

	require.Equal(t, 250*time.Millisecond, opts.flushInterval)

	agg := opts.aggregatorFactory(context.Background(), opts.aggregationOption...)
	require.True(t, factoryCalled)

	events, err := agg.Append(context.Background(), aguievents.NewTextMessageContentEvent("msg", "hi"))
	require.NoError(t, err)
	require.Len(t, events, 1) // disabled aggregation should pass through.
}
