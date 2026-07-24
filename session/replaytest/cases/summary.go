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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// SummaryGenerateUpdate is case 6: full-session summary written twice
// (v2 overwrites v1), a branch-filtered summary that must stay isolated,
// and a second session proving summaries are attributed per session.
// It guards overwrite semantics, filter-key isolation and session
// attribution of summaries.
func SummaryGenerateUpdate() replaytest.Case {
	return replaytest.Case{
		Name: "summary/generate_update",
		Description: "summary overwrite (v2 replaces v1), filter-key isolation, " +
			"per-session attribution, boundary/version fields",
		NeedCaps: replaytest.Capability{Session: true, Summary: true},
		Steps: []replaytest.Step{
			createSession("sum-a"),
			userMsg("sum-a", "inv-sa-1", "我们讨论一下长期记忆设计"),
			assistantMsg("sum-a", "inv-sa-1", "好的，先从存储模型说起。"),
			userMsg("sum-a", "inv-sa-2", "branch-a 方案呢？"),
			summaryStep("sum-a", ""),
			assistantMsg("sum-a", "inv-sa-2", "branch-a 走增量摘要。"),
			// Overwrite the full-session summary.
			summaryStep("sum-a", ""),
			// Isolated filter-key summary; must not clobber the full one.
			summaryStep("sum-a", "branch-a"),
			// A second session proves attribution.
			createSession("sum-b"),
			userMsg("sum-b", "inv-sb-1", "另一个会话的消息"),
			summaryStep("sum-b", ""),
		},
	}
}

// SummaryTruncationRetain is case 7: twenty events, a summary whose
// boundary anchors inside the history, then three fresh events. The
// snapshot reads the session back twice: the full event list, and the
// truncated context-window view (session.WithEventNum) that a replay of a
// compacted conversation actually loads. Both views must be coherent
// across backends: the truncated view keeps the summary plus the retained
// tail events, and the boundary stays anchored on the last summarized
// event even though that event falls outside the retained window.
func SummaryTruncationRetain() replaytest.Case {
	steps := []replaytest.Step{createSession("sum-t")}
	for i := 1; i <= 20; i++ {
		inv := fmt.Sprintf("inv-st-%02d", i)
		steps = append(steps, userMsg("sum-t", inv, fmt.Sprintf("history-%02d", i)))
	}
	steps = append(steps, summaryStep("sum-t", ""))
	for i := 21; i <= 23; i++ {
		inv := fmt.Sprintf("inv-st-%02d", i)
		steps = append(steps, userMsg("sum-t", inv, fmt.Sprintf("fresh-%02d", i)))
	}
	return replaytest.Case{
		Name: "summary/truncation_retain",
		Description: "long history summarized, then new events appended; " +
			"full and truncated (WithEventNum) read-backs keep summary + " +
			"retained events coherent, boundary anchored outside the window",
		NeedCaps:       replaytest.Capability{Session: true, Summary: true},
		WindowEventNum: 3,
		Steps:          steps,
	}
}
