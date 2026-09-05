//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	contextCompactionEventsSink []event.Event
	contextCompactionStatsSink  ContextCompactionStats
)

func BenchmarkContextCompaction(b *testing.B) {
	for _, tc := range []struct {
		name        string
		eventCount  int
		resultBytes int
		enabled     bool
	}{
		{name: "disabled", eventCount: 256, resultBytes: 4096},
		{name: "enabled-short", eventCount: 16, resultBytes: 4096, enabled: true},
		{name: "enabled-typical", eventCount: 256, resultBytes: 4096, enabled: true},
	} {
		name := fmt.Sprintf(
			"mode=%s/events=%d/result_bytes=%d",
			tc.name,
			tc.eventCount,
			tc.resultBytes,
		)
		b.Run(name, func(b *testing.B) {
			events := contextCompactionBenchmarkEvents(tc.eventCount, tc.resultBytes)
			cfg := ContextCompactionConfig{
				Enabled:             tc.enabled,
				KeepRecentRequests:  1,
				ToolResultMaxTokens: 256,
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				contextCompactionEventsSink, contextCompactionStatsSink = compactIncrementEvents(
					context.Background(),
					events,
					"request-current",
					"invocation-current",
					cfg,
				)
			}
		})
	}
}

func contextCompactionBenchmarkEvents(count int, resultBytes int) []event.Event {
	events := make([]event.Event, count)
	content := strings.Repeat("r", resultBytes)
	for i := range events {
		requestID := fmt.Sprintf("request-%d", i)
		invocationID := fmt.Sprintf("invocation-%d", i)
		if i == len(events)-1 {
			requestID = "request-current"
			invocationID = "invocation-current"
		}
		events[i] = event.Event{
			ID:           fmt.Sprintf("event-%d", i),
			RequestID:    requestID,
			InvocationID: invocationID,
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage(
						fmt.Sprintf("tool-call-%d", i),
						"benchmark_tool",
						content,
					),
				}},
			},
		}
	}
	return events
}
