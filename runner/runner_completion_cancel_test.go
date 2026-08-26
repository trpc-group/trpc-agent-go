//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestRunnerCompletionCancelledBeforeFirstEventNoRace guards against a data
// race between the agent goroutine's asynchronous invocation setup (which
// writes Invocation.AgentName via setupInvocation) and the runner completion
// cleanup. When the context is cancelled before the first agent event arrives,
// runEventLoop returns via ctx.Done() with no channel happens-before edge
// against the agent goroutine, so the cleanup must not read the live
// invocation. Run with -race; the previous implementation raced inside
// agent.EmitEvent's read of inv.AgentName.
func TestRunnerCompletionCancelledBeforeFirstEventNoRace(t *testing.T) {
	ag := chainagent.New("chain")
	successful := 0
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := NewRunner("app", ag)
		ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("hi"))
		if err != nil {
			continue
		}
		successful++
		for range ch {
		}
	}
	// Require at least one run to reach the event loop so the completion
	// cleanup path is actually exercised.
	require.NotZero(t, successful)
}
