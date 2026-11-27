//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package anthropic

import (
	"encoding/json"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBuildStreamingFinalResponse_MarksExclusivePromptCacheAccounting(t *testing.T) {
	var acc anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{"id":"m1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":80,"cache_read_input_tokens":20}}`), &acc))
	final := buildStreamingFinalResponse(acc)
	require.NotNil(t, final.Usage)
	assert.Equal(t, 100, final.Usage.PromptTokens)
	assert.Equal(t, 80, final.Usage.PromptTokensDetails.CacheCreationTokens)
	assert.Equal(t, model.PromptCacheAccountingExclusive, final.Usage.PromptCacheAccounting)
	assert.Equal(t, model.PromptCacheAccountingExclusive, final.Clone().Usage.PromptCacheAccounting)

	var noCache anthropic.Message
	require.NoError(t, json.Unmarshal([]byte(`{"id":"m2","type":"message","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":2}}`), &noCache))
	final = buildStreamingFinalResponse(noCache)
	require.NotNil(t, final.Usage)
	assert.Equal(t, model.PromptCacheAccountingUnspecified, final.Usage.PromptCacheAccounting)
}
