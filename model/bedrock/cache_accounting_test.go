//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/usagemeta"
)

func TestBuildNonStreamingResponse_MarksExclusivePromptCacheAccounting(t *testing.T) {
	m := New("test-model")
	output := &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role:    types.ConversationRoleAssistant,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "ok"}},
		}},
		Usage: &types.TokenUsage{
			InputTokens:           aws.Int32(100),
			OutputTokens:          aws.Int32(10),
			TotalTokens:           aws.Int32(190),
			CacheWriteInputTokens: aws.Int32(80),
		},
	}
	rsp := m.buildNonStreamingResponse(output)
	require.NotNil(t, rsp.Usage)
	assert.Equal(t, 80, rsp.Usage.PromptTokensDetails.CacheCreationTokens)
	assert.Equal(t, usagemeta.PromptCacheAccountingExclusive, usagemeta.GetPromptCacheAccounting(rsp.Usage))

	output.Usage = &types.TokenUsage{InputTokens: aws.Int32(10), OutputTokens: aws.Int32(2), TotalTokens: aws.Int32(12)}
	rsp = m.buildNonStreamingResponse(output)
	require.NotNil(t, rsp.Usage)
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(rsp.Usage))
}
