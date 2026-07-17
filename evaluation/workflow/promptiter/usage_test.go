//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeUsageAggregatesValuesAndCompleteness(t *testing.T) {
	actual := MergeUsage(
		Usage{Calls: 1, PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8, Complete: true},
		Usage{Calls: 2, PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18, Complete: false},
	)
	assert.Equal(t, Usage{Calls: 3, PromptTokens: 10, CompletionTokens: 16, TotalTokens: 26}, actual)
}

func TestMergeUsageDerivesMissingTotalAndUsageEvidence(t *testing.T) {
	actual := MergeUsage(Usage{PromptTokens: 3, CompletionTokens: 5, Complete: true})
	assert.Equal(t, int64(8), actual.TotalTokens)
	assert.True(t, actual.Complete)

}
