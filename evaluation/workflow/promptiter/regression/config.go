//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

func cloneConfig(config Config) Config {
	cloned := config
	cloned.Candidates = append([]CandidateConfig(nil), config.Candidates...)
	for i := range cloned.Candidates {
		cloned.Candidates[i].AddressCategories = append(
			[]FailureCategory(nil),
			config.Candidates[i].AddressCategories...,
		)
	}
	cloned.Gate.CriticalCaseIDs = append([]string(nil), config.Gate.CriticalCaseIDs...)
	cloned.Gate.MaxNewFailures = clonePointer(config.Gate.MaxNewFailures)
	cloned.Gate.MaxPerCaseScoreDrop = clonePointer(config.Gate.MaxPerCaseScoreDrop)
	cloned.Gate.MaxCostUSD = clonePointer(config.Gate.MaxCostUSD)
	cloned.Gate.MaxCostIncreaseRatio = clonePointer(config.Gate.MaxCostIncreaseRatio)
	cloned.Gate.MaxModelCalls = clonePointer(config.Gate.MaxModelCalls)
	cloned.Gate.MaxTotalCalls = clonePointer(config.Gate.MaxTotalCalls)
	cloned.Gate.MaxLatencyMS = clonePointer(config.Gate.MaxLatencyMS)
	return cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
