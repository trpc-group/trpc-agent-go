//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageMeterZeroValueRecordsAndAccumulatesUsage(t *testing.T) {
	var meter UsageMeter
	first := ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 1},
		InputTokens:  Count{Available: true, Value: 10},
		OutputTokens: Count{Available: true, Value: 20},
		LatencyMS:    Count{Available: true, Value: 30},
		MonetaryCost: Amount{Available: true, Value: 1.25, Unit: "USD"},
	}
	second := ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 2},
		InputTokens:  Count{Available: true, Value: 11},
		OutputTokens: Count{Available: true, Value: 21},
		LatencyMS:    Count{Available: true, Value: 31},
		MonetaryCost: Amount{Available: true, Value: 0.75, Unit: "USD"},
	}

	meter.Record(first)
	require.Equal(t, first, meter.Snapshot())

	meter.Record(second)
	require.Equal(t, ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 3},
		InputTokens:  Count{Available: true, Value: 21},
		OutputTokens: Count{Available: true, Value: 41},
		LatencyMS:    Count{Available: true, Value: 61},
		MonetaryCost: Amount{Available: true, Value: 2, Unit: "USD"},
	}, meter.Snapshot())
}

func TestUsageMeterZeroValueDoesNotInventUnavailableIncrement(t *testing.T) {
	var meter UsageMeter

	meter.Record(ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 1},
		InputTokens:  Count{},
		OutputTokens: Count{Available: true, Value: 2},
		LatencyMS:    Count{Available: true, Value: 3},
	})

	require.Equal(t, ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 1},
		InputTokens:  Count{},
		OutputTokens: Count{Available: true, Value: 2},
		LatencyMS:    Count{Available: true, Value: 3},
	}, meter.Snapshot())
}

func TestResourceUsageDeltaPreservesFirstMonetaryCost(t *testing.T) {
	after := ResourceUsage{
		MonetaryCost: Amount{Available: true, Value: 1.25, Unit: "USD"},
	}

	delta := resourceUsageDelta(ResourceUsage{}, after)

	require.Equal(t, after.MonetaryCost, delta.MonetaryCost)
}

func TestResourceUsageDeltaRejectsInvalidUnavailableMonetaryBaseline(t *testing.T) {
	before := ResourceUsage{
		MonetaryCost: Amount{Value: 0.25, Unit: "USD"},
	}
	after := ResourceUsage{
		MonetaryCost: Amount{Available: true, Value: 1.25, Unit: "USD"},
	}

	delta := resourceUsageDelta(before, after)

	require.Equal(t, Amount{}, delta.MonetaryCost)
}
