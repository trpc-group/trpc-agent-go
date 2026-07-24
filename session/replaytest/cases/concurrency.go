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

// ConcurrencyInterleavedAppend is case 9: three goroutines each append
// five events behind a start barrier, tagged by branch. Global ordering of
// concurrent writers is legitimately backend-dependent, so comparison uses
// multiset equality plus per-branch partial order.
func ConcurrencyInterleavedAppend() replaytest.Case {
	return replaytest.Case{
		Name: "concurrency/interleaved_append",
		Description: "three concurrent writers interleave appends; " +
			"multiset equality plus per-branch partial order",
		NeedCaps:        replaytest.Capability{Session: true},
		UnorderedEvents: true,
		Steps: []replaytest.Step{
			createSession("conc-1"),
			// A leading user message keeps every worker event visible under
			// read-side filtering (events before the first user message are
			// dropped on read by design).
			userMsg("conc-1", "inv-c1-0", "并发写入开始"),
			{
				Op:        replaytest.OpConcurrentEvents,
				SessionID: "conc-1",
				Concurrent: []replaytest.WriterSpec{
					{Branch: "worker-1", Prefix: "w1-msg", Count: 5},
					{Branch: "worker-2", Prefix: "w2-msg", Count: 5},
					{Branch: "worker-3", Prefix: "w3-msg", Count: 5},
				},
			},
		},
	}
}
