//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"trpc.group/trpc-go/trpc-agent-go/internal/usagemeta"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGemini_MarksInclusivePromptCacheAccounting(t *testing.T) {
	m := &Model{name: "gemini-test"}
	u := m.completionUsageToModelUsage(&genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        100,
		CandidatesTokenCount:    10,
		TotalTokenCount:         110,
		CachedContentTokenCount: 80,
	})
	require.NotNil(t, u)
	assert.Equal(t, usagemeta.PromptCacheAccountingInclusive, usagemeta.GetPromptCacheAccounting(u))

	acc := &Accumulator{}
	acc.Accumulate(&model.Response{Usage: u})
	acc.Accumulate(&model.Response{Usage: &model.Usage{}})
	final := acc.BuildResponse()
	require.NotNil(t, final.Usage)
	assert.Equal(t, 80, final.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, usagemeta.PromptCacheAccountingInclusive, usagemeta.GetPromptCacheAccounting(final.Usage))

	plain := m.completionUsageToModelUsage(&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10})
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(plain))
}
