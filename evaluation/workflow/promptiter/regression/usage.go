//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"math"
)

func validateUsage(usage Usage) error {
	if usage.ModelCalls < 0 {
		return errors.New("model calls cannot be negative")
	}
	if usage.ToolCalls < 0 {
		return errors.New("tool calls cannot be negative")
	}
	if usage.InputTokens < 0 {
		return errors.New("input tokens cannot be negative")
	}
	if usage.OutputTokens < 0 {
		return errors.New("output tokens cannot be negative")
	}
	if math.IsNaN(usage.CostUSD) || math.IsInf(usage.CostUSD, 0) || usage.CostUSD < 0 {
		return errors.New("cost must be finite and non-negative")
	}
	if usage.LatencyMS < 0 {
		return errors.New("latency cannot be negative")
	}
	return nil
}

func checkedAddInt(left, right int) (int, error) {
	maxInt := int(^uint(0) >> 1)
	if left < 0 || right < 0 {
		return 0, errors.New("operands cannot be negative")
	}
	if left > maxInt-right {
		return 0, errors.New("integer overflow")
	}
	return left + right, nil
}

func checkedAddInt64(left, right int64) (int64, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if left < 0 || right < 0 {
		return 0, errors.New("operands cannot be negative")
	}
	if left > maxInt64-right {
		return 0, errors.New("integer overflow")
	}
	return left + right, nil
}
