//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

// Usage records model-call telemetry observed by one PromptIter stage.
type Usage struct {
	Calls            int
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	// Complete is true only when every model-bearing call in the stage exposed
	// usage metadata. A zero-call deterministic stage should set Complete true.
	Complete bool
}

// MergeUsage combines stage telemetry without hiding incomplete contributors.
func MergeUsage(values ...Usage) Usage {
	result := Usage{Complete: true}
	for _, value := range values {
		result.Calls += value.Calls
		result.PromptTokens += value.PromptTokens
		result.CompletionTokens += value.CompletionTokens
		result.TotalTokens += value.TotalTokens
		result.Complete = result.Complete && value.Complete
	}
	if result.TotalTokens == 0 {
		result.TotalTokens = result.PromptTokens + result.CompletionTokens
	}
	return result
}
