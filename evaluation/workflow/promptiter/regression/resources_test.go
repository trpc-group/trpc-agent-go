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
