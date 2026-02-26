//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestChatMetricsTracker_RecordMetrics_NoMetrics_ReturnsNoop(t *testing.T) {
	originalRequestCnt := ChatMetricTRPCAgentGoClientRequestCnt
	originalTokenUsage := ChatMetricGenAIClientTokenUsage
	originalOperationDuration := ChatMetricGenAIClientOperationDuration
	originalServerTimeToFirstToken := ChatMetricGenAIServerTimeToFirstToken
	originalClientTimeToFirstToken := ChatMetricTRPCAgentGoClientTimeToFirstToken
	originalTimePerOutputToken := ChatMetricTRPCAgentGoClientTimePerOutputToken
	originalOutputTokenPerTime := ChatMetricTRPCAgentGoClientOutputTokenPerTime
	t.Cleanup(func() {
		ChatMetricTRPCAgentGoClientRequestCnt = originalRequestCnt
		ChatMetricGenAIClientTokenUsage = originalTokenUsage
		ChatMetricGenAIClientOperationDuration = originalOperationDuration
		ChatMetricGenAIServerTimeToFirstToken = originalServerTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimeToFirstToken = originalClientTimeToFirstToken
		ChatMetricTRPCAgentGoClientTimePerOutputToken = originalTimePerOutputToken
		ChatMetricTRPCAgentGoClientOutputTokenPerTime = originalOutputTokenPerTime
	})

	ChatMetricTRPCAgentGoClientRequestCnt = nil
	ChatMetricGenAIClientTokenUsage = nil
	ChatMetricGenAIClientOperationDuration = nil
	ChatMetricGenAIServerTimeToFirstToken = nil
	ChatMetricTRPCAgentGoClientTimeToFirstToken = nil
	ChatMetricTRPCAgentGoClientTimePerOutputToken = nil
	ChatMetricTRPCAgentGoClientOutputTokenPerTime = nil

	tracker := NewChatMetricsTracker(context.Background(), nil, nil, &model.TimingInfo{}, nil, nil)
	recordFunc := tracker.RecordMetrics()
	require.NotNil(t, recordFunc)
	require.NotPanics(t, recordFunc)
}

func TestChatMetricsTracker_TrackResponse_ReasoningDuration_UsesLazyNow(t *testing.T) {
	ctx := context.Background()
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	timingInfo := &model.TimingInfo{}
	tracker := NewChatMetricsTracker(ctx, nil, req, timingInfo, nil, nil)

	tracker.TrackResponse(&model.Response{})
	require.False(t, tracker.isFirstToken, "expected first token to be consumed")

	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{ReasoningContent: "r1"},
			},
		},
	})
	require.False(t, tracker.firstReasoningTime.IsZero(), "expected reasoning start time to be recorded")
	require.Equal(t, tracker.firstReasoningTime, tracker.lastReasoningTime, "expected first reasoning chunk to update last time")

	time.Sleep(10 * time.Millisecond)
	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{ReasoningContent: "r2"},
			},
		},
	})
	require.True(t, tracker.lastReasoningTime.After(tracker.firstReasoningTime), "expected reasoning time to advance")

	tracker.TrackResponse(&model.Response{
		Choices: []model.Choice{
			{
				Delta: model.Message{Content: "done"},
			},
		},
	})
	require.Greater(t, timingInfo.ReasoningDuration, time.Duration(0), "expected reasoning duration to be recorded")
}
