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

// TrackToolAndSubtask is case 8: three track events across two tracks —
// tool latency (duration scrubbed by the normalizer), an error record and
// a subtask status. It guards track-name isolation, time-series order and
// payload fidelity including error fields.
func TrackToolAndSubtask() replaytest.Case {
	return replaytest.Case{
		Name: "track/tool_and_subtask",
		Description: "track events for tool latency, error record and subtask status; " +
			"duration fields are scrubbed during normalization",
		NeedCaps: replaytest.Capability{Session: true, Tracks: true},
		Steps: []replaytest.Step{
			createSession("track-1"),
			userMsg("track-1", "inv-tr-1", "跑一个搜索子任务"),
			assistantMsg("track-1", "inv-tr-1", "已启动子任务。"),
			{Op: replaytest.OpAppendTrack, SessionID: "track-1",
				Track: &replaytest.TrackSpec{
					Track:   "tool_call",
					Payload: `{"tool":"search","status":"ok","duration_ms":123.4}`,
				}},
			{Op: replaytest.OpAppendTrack, SessionID: "track-1",
				Track: &replaytest.TrackSpec{
					Track:   "tool_call",
					Payload: `{"tool":"search","status":"error","error":"timeout","duration_ms":987.6}`,
				}},
			{Op: replaytest.OpAppendTrack, SessionID: "track-1",
				Track: &replaytest.TrackSpec{
					Track:   "subtask",
					Payload: `{"task":"t-1","status":"done","invocation":"inv-tr-1"}`,
				}},
		},
	}
}
