//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"math"
	"strings"
)

func normalizeUsageSummary(source UsageSummary) (UsageSummary, error) {
	if source.Calls < 0 || source.InputTokens < 0 || source.OutputTokens < 0 ||
		source.TotalTokens < 0 || source.Latency < 0 {
		return UsageSummary{}, errors.New("usage counts and latency must be non-negative")
	}
	if math.IsNaN(source.EstimatedCost) || math.IsInf(source.EstimatedCost, 0) ||
		source.EstimatedCost < 0 {
		return UsageSummary{}, errors.New("estimated cost must be finite and non-negative")
	}
	if !source.CostKnown && source.EstimatedCost != 0 {
		return UsageSummary{}, errors.New("estimated cost is set while cost is marked unknown")
	}
	observedTokens := source.InputTokens + source.OutputTokens
	if observedTokens < source.InputTokens || observedTokens < source.OutputTokens {
		return UsageSummary{}, errors.New("usage token total overflows int64")
	}
	if source.TotalTokens == 0 {
		source.TotalTokens = observedTokens
	} else if source.TotalTokens < observedTokens {
		return UsageSummary{}, errors.New("total tokens are smaller than input plus output tokens")
	}
	if source.Complete && strings.TrimSpace(source.Source) == "" {
		return UsageSummary{}, errors.New("complete usage summary requires a source")
	}
	return source, nil
}
