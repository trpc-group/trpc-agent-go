//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenario

import "time"

// 固定回放基准时间，避免 SQLite 按当前时间过滤摘要。
var replayBaseTime = time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)

//单轮普通对话

var Case01_SingleTurn = &Case{
	Name: "case01_single_turn",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "你好"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "你好，有什么可以帮你？"},
	},
}

// 多轮普通对话
var Case02_MultiTurn = &Case{
	Name: "case02_multi_turn",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "你好"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "你好，有什么可以帮你？"},
		{Kind: OpAppendEvent, Role: "user", Content: "帮我写一个 Go 测试"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "可以，先准备输入和期望输出。"},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "武汉刮台风了"},
	},
}

// 会话状态更新
var Case03_UpdateState = &Case{
	Name: "case03_update_state",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpUpdateState, State: map[string]string{"weather": "晴天", "temporary": "remove-me"}},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "今天天气晴天"},
		{Kind: OpUpdateState, State: map[string]string{"weather": "下雨"}},
		{Kind: OpDeleteState, DeleteState: []string{"temporary"}},
		{Kind: OpAppendEvent, Role: "user", Content: "今天天气怎么样？"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "今天天气下雨"},
		{Kind: OpClearState},
		{Kind: OpUpdateState, State: map[string]string{"final": "clean"}},
	},
}

// 工具调用与响应
var Case04_ToolCall = &Case{
	Name: "case04_tool_call",
	Ops: []Op{
		{Kind: OpCreateSession},
		{
			Kind: OpAppendEvent, Role: "user", Author: "user",
			Content: "查一下北京天气", Branch: "main", Tag: "weather",
		},
		{
			Kind:     OpAppendToolCall,
			ToolID:   "call_weather_1",
			ToolName: "weather_query",
			ToolArgs: `{"city":"北京"}`,
			Author:   "assistant",
			Branch:   "main",
		},
		{
			Kind:     OpAppendToolResponse,
			ToolID:   "call_weather_1",
			ToolName: "weather_query",
			Content:  `{"weather":"晴"}`,
			Author:   "tool",
			Branch:   "main",
			Extensions: map[string]string{
				"trpc_agent.tool_call_args": `{"call_weather_1":{"city":"北京"}}`,
			},
		},
		{
			Kind: OpAppendEvent, Role: "assistant", Author: "assistant",
			Content: "北京今天晴。", Branch: "main",
			StateDelta: map[string]string{"last_weather": "晴"},
		},
	},
}

// 全量 Summary
var Case06_Summary = &Case{
	Name: "case06_summary",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "用户喜欢简洁中文回答"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "好的，后续会尽量简洁。"},
		{Kind: OpUpdateSummary, FilterKey: "", Force: true},
		{Kind: OpAppendEvent, Role: "user", Content: "请继续保持简洁"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "好的。"},
		{Kind: OpUpdateSummary, FilterKey: "", Force: true},
	},
}

// 按 FilterKey 生成 Summary，验证分支隔离
var Case06_SummaryFilterKey = &Case{
	Name: "case06_summary_filter_key",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "查一下北京天气", FilterKey: "weather"},
		{Kind: OpAppendToolCall, ToolID: "call_weather_2", ToolName: "weather_query", ToolArgs: `{"city":"北京"}`, FilterKey: "weather"},
		{Kind: OpAppendToolResponse, ToolID: "call_weather_2", ToolName: "weather_query", Content: `{"weather":"晴"}`, FilterKey: "weather"},
		{Kind: OpAppendEvent, Role: "assistant", Content: "北京今天晴。", FilterKey: "weather"},
		{Kind: OpAppendEvent, Role: "user", Content: "我喜欢 Go", FilterKey: "profile"},
		{Kind: OpUpdateSummary, FilterKey: "weather", Force: true},
	},
}

// Summary 后截断事件读取
var Case07_SummaryWithTruncation = &Case{
	Name:          "case07_summary_with_truncation",
	FinalEventNum: 3,
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, EventID: "case07-u1", Timestamp: replayBaseTime, Role: "user", Content: "第一轮问题"},
		{Kind: OpAppendEvent, EventID: "case07-a1", Timestamp: replayBaseTime.Add(time.Second), Role: "assistant", Content: "第一轮回答"},
		{Kind: OpAppendEvent, EventID: "case07-u2", Timestamp: replayBaseTime.Add(2 * time.Second), Role: "user", Content: "第二轮问题"},
		{Kind: OpAppendEvent, EventID: "case07-a2", Timestamp: replayBaseTime.Add(3 * time.Second), Role: "assistant", Content: "第二轮回答"},
		{Kind: OpUpdateSummary, FilterKey: "", Force: true},
		{Kind: OpAppendEvent, EventID: "case07-u3", Timestamp: replayBaseTime.Add(4 * time.Second), Role: "user", Content: "摘要后的新问题"},
		{Kind: OpAppendEvent, EventID: "case07-a3", Timestamp: replayBaseTime.Add(5 * time.Second), Role: "assistant", Content: "摘要后的新回答"},
	},
}

// Track 事件：正常、完成、耗时、失败
var Case08_Track = &Case{
	Name: "case08_track",
	Ops: []Op{
		{Kind: OpCreateSession},
		{Kind: OpAppendEvent, Role: "user", Content: "执行天气工具"},
		// 正常
		{
			Kind:      OpAppendTrack,
			TrackName: "tool.weather_query",
			TrackPayload: `{
				"event_type":"start",
				"invocation_id":"inv-1",
				"tool":"weather_query",
				"status":"running"
			}`,
		},
		// 完成
		{
			Kind:      OpAppendTrack,
			TrackName: "subtask.weather_parse",
			TrackPayload: `{
				"event_type":"state",
				"invocation_id":"inv-1",
				"subtask":"parse_response",
				"status":"done"
			}`,
		},
		// 延时
		{
			Kind:      OpAppendTrack,
			TrackName: "tool.weather_query",
			TrackPayload: `{
				"event_type":"finish",
				"invocation_id":"inv-1",
				"status":"ok",
				"duration_ms":25
			}`,
		},
		// 超时 + 失败
		{
			Kind:      OpAppendTrack,
			TrackName: "tool.weather_query",
			TrackPayload: `{
				"event_type":"error",
				"invocation_id":"inv-2",
				"status":"error",
				"error":"timeout",
				"duration_ms":3000
			}`,
		},
	},
}

// 并发追加事件，验证最终顺序稳定
var Case09_ConcurrentAppend = &Case{
	Name: "case09_concurrent_append",
	Ops: []Op{
		{Kind: OpCreateSession},
		{
			Kind: OpConcurrentAppend,
			Concurrent: []Op{
				{
					Kind: OpAppendEvent, EventID: "case09-u1",
					Timestamp: replayBaseTime.Add(10 * time.Second),
					Role:      "user", Content: "并发任务一", DelayMS: 1,
				},
				{
					Kind: OpAppendEvent, EventID: "case09-a1",
					Timestamp: replayBaseTime.Add(11 * time.Second),
					Role:      "assistant", Content: "并发任务一完成", DelayMS: 10,
				},
				{
					Kind: OpAppendEvent, EventID: "case09-u2",
					Timestamp: replayBaseTime.Add(12 * time.Second),
					Role:      "user", Content: "并发任务二", DelayMS: 20,
				},
			},
		},
	},
}

// 仅 StateDelta 恢复，不附带完整响应事件
var Case10_Recovery = &Case{
	Name: "case10_recovery",
	Ops: []Op{
		{Kind: OpCreateSession},
		{
			Kind: OpAppendEventWithRetry, EventID: "case10-u1",
			Timestamp: replayBaseTime.Add(21 * time.Second),
			Role:      "user", Content: "重试后的正常事件",
		},
		{Kind: OpUpdateState, State: map[string]string{"recovered": "ok"}},
	},
}
