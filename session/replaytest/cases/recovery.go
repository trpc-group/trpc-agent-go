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

// RecoveryDirtyRetry is case 10: a transiently failing append retried to
// success (the retried event with its state delta must be stored once, not
// duplicated), a duplicate-content retry that both backends must store
// exactly twice (no silent dedup), a forced summary after the retried and
// duplicated writes (summary generation and session attribution must agree
// after retry/duplicate writes), a duplicate memory write retried by the
// client that both backends must handle identically, and an expected error
// whose class must agree. Backend-level dirty half-writes (event persisted,
// delta lost) are covered by the fault-injection tests in
// e2e_fault_test.go.
func RecoveryDirtyRetry() replaytest.Case {
	return replaytest.Case{
		Name: "recovery/dirty_retry",
		Description: "transient failure + retry stores the event once; " +
			"duplicate retry stored consistently on both backends; " +
			"summary after retried/duplicate writes stays consistent; " +
			"duplicate memory write handled identically on both backends; " +
			"error class agreement",
		NeedCaps: replaytest.Capability{Session: true, Memory: true, Summary: true},
		Steps: []replaytest.Step{
			createSession("rec-1"),
			userMsg("rec-1", "inv-r1-0", "开始"),
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "rec-1",
				Event: &replaytest.EventSpec{
					Author:       "assistant",
					Role:         "assistant",
					Content:      "retry-me",
					InvocationID: "inv-r1-1",
					FailTimes:    1,
					StateDelta: map[string]string{
						"retry:key": `"applied-once"`,
					},
				},
			},
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "rec-1",
				Event: &replaytest.EventSpec{
					Author:       "user",
					Role:         "user",
					Content:      "idempotent-content",
					InvocationID: "inv-r1-2",
				},
			},
			// Client retry with a regenerated event ID: same content again.
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "rec-1",
				Event: &replaytest.EventSpec{
					Author:       "user",
					Role:         "user",
					Content:      "idempotent-content",
					InvocationID: "inv-r1-2",
				},
			},
			// A forced summary after the retried and duplicated writes:
			// generation and session attribution must agree on both
			// backends.
			summaryStep("rec-1", ""),
			// Client failure-retry resubmits the same memory twice; both
			// backends must handle the duplicate identically.
			{
				Op:        replaytest.OpAddMemory,
				SessionID: "rec-1",
				Memory: &replaytest.MemorySpec{
					Content: "恢复场景：重试后写入的记忆",
					Topics:  []string{"recovery"},
				},
			},
			{
				Op:        replaytest.OpAddMemory,
				SessionID: "rec-1",
				Memory: &replaytest.MemorySpec{
					Content: "恢复场景：重试后写入的记忆",
					Topics:  []string{"recovery"},
				},
			},
			// Appending with an empty session ID must fail key validation
			// on every backend; the error class is compared.
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "",
				Event: &replaytest.EventSpec{
					Author:      "user",
					Role:        "user",
					Content:     "must-fail",
					ExpectError: true,
				},
			},
		},
	}
}
