//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package cases contains the public replay-consistency cases. Every backend
// binding imports this package and replays All() against its target; adding
// a backend never requires changing a case.
package cases

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// All returns every public replay case.
func All() []replaytest.Case {
	return []replaytest.Case{
		SingleTurn(),
		MultiTurnOrder(),
		ToolCallFullCycle(),
		StateOverwriteDeleteClear(),
		MemoryWriteRead(),
		MemoryScopeIsolation(),
		SummaryGenerateUpdate(),
		SummaryTruncationRetain(),
		TrackToolAndSubtask(),
		ConcurrencyInterleavedAppend(),
		RecoveryDirtyRetry(),
	}
}

// createSession builds a create-session step.
func createSession(sid string) replaytest.Step {
	return replaytest.Step{Op: replaytest.OpCreateSession, SessionID: sid}
}

// createSessionWithState builds a create-session step with initial state.
func createSessionWithState(sid string, state map[string]string) replaytest.Step {
	return replaytest.Step{Op: replaytest.OpCreateSession, SessionID: sid, State: state}
}

// userMsg builds a user message append step.
func userMsg(sid, inv, content string) replaytest.Step {
	return replaytest.Step{
		Op:        replaytest.OpAppendEvent,
		SessionID: sid,
		Event: &replaytest.EventSpec{
			Author:       "user",
			Role:         "user",
			Content:      content,
			InvocationID: inv,
		},
	}
}

// assistantMsg builds an assistant message append step.
func assistantMsg(sid, inv, content string) replaytest.Step {
	return replaytest.Step{
		Op:        replaytest.OpAppendEvent,
		SessionID: sid,
		Event: &replaytest.EventSpec{
			Author:       "assistant",
			Role:         "assistant",
			Content:      content,
			InvocationID: inv,
			FinishReason: "stop",
		},
	}
}

// summaryStep builds a forced summary step.
func summaryStep(sid, filterKey string) replaytest.Step {
	return replaytest.Step{
		Op:        replaytest.OpSummary,
		SessionID: sid,
		Summary:   &replaytest.SummarySpec{FilterKey: filterKey},
	}
}

// seqEvents builds n alternating user/assistant turns with numbered content.
func seqEvents(sid string, n int) []replaytest.Step {
	var steps []replaytest.Step
	for i := 0; i < n; i++ {
		inv := fmt.Sprintf("inv-%s-%02d", sid, i+1)
		steps = append(steps,
			userMsg(sid, inv, fmt.Sprintf("seq-%02d-user", i+1)),
			assistantMsg(sid, inv, fmt.Sprintf("seq-%02d-assistant", i+1)),
		)
	}
	return steps
}
