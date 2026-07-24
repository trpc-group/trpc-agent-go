//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cases

import (
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// ToolCallFullCycle is case 3: assistant tool call (nested JSON args with a
// float), tool response referencing the call ID, assistant follow-up. The
// events exercise branch, tag, filterKey, stateDelta and extensions.
// It guards tool-call/response pairing, argument fidelity after JSON
// canonicalization, and the five metadata fields.
func ToolCallFullCycle() replaytest.Case {
	return replaytest.Case{
		Name: "toolcall/full_cycle",
		Description: "tool call + tool response + args canonicalization; " +
			"branch/tag/filterKey/stateDelta/extensions fidelity",
		NeedCaps: replaytest.Capability{Session: true},
		Steps: []replaytest.Step{
			createSession("tool-1"),
			userMsg("tool-1", "inv-t1-1", "查一下深圳明天的天气"),
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "tool-1",
				Event: &replaytest.EventSpec{
					Author:       "assistant",
					Role:         "assistant",
					InvocationID: "inv-t1-1",
					RequestID:    "req-t1-1",
					Branch:       "root.weather",
					Tag:          "tool-call",
					FilterKey:    "weather",
					ToolCalls: []replaytest.ToolCallSpec{{
						ID:   "call-weather-1",
						Name: "get_weather",
						Args: `{"city":"深圳","days":1.5,"options":{"unit":"c","detail":true}}`,
					}},
					StateDelta: map[string]string{
						"tool:last":  `"get_weather"`,
						"tool:count": "1",
					},
					Extensions: map[string]string{
						"trace.id": `"trace-abc-001"`,
						"metrics":  `{"duration_ms": 42, "retries": 0}`,
					},
				},
			},
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "tool-1",
				Event: &replaytest.EventSpec{
					Author:       "get_weather",
					Role:         "tool",
					InvocationID: "inv-t1-1",
					Branch:       "root.weather",
					Tag:          "tool-response",
					FilterKey:    "weather",
					ToolID:       "call-weather-1",
					ToolName:     "get_weather",
					Content:      `{"temp":26.5,"unit":"c","text":"晴"}`,
				},
			},
			assistantMsg("tool-1", "inv-t1-1", "深圳明天晴，26.5°C。"),
		},
	}
}
