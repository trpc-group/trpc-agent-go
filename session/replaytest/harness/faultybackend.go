//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
)

// RunFaulty replays a case against a fault-carrying backend built from b. The
// fault is injected at the real write boundary, so the read-back path is
// identical to a clean run — the harness proves it can catch genuinely
// persisted bad data.
func RunFaulty(ctx context.Context, b *backends.Backend, c *ReplayCase) (*Snapshot, error) {
	if c.FaultInjection == backends.FaultOverwriteSummary {
		return nil, fmt.Errorf("overwrite_summary must be exercised via a faulty-summarizer backend, not RunFaulty")
	}
	fb, err := backends.WrapFaulty(b, c.FaultInjection)
	if err != nil {
		return nil, err
	}
	return Run(ctx, fb, c)
}
