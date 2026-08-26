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
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/debuglog"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// cancellationIgnoringAgent ignores cancellation: its event stream is never
// closed, and it writes Invocation.AgentName late, mimicking setupInvocation of
// a misbehaving agent. It exercises the drain timeout path of the completion
// cleanup.
type cancellationIgnoringAgent struct {
	name string
}

func (a *cancellationIgnoringAgent) Info() agent.Info { return agent.Info{Name: a.name} }
func (a *cancellationIgnoringAgent) SubAgents() []agent.Agent {
	return nil
}
func (a *cancellationIgnoringAgent) FindSubAgent(name string) agent.Agent {
	return nil
}
func (a *cancellationIgnoringAgent) Tools() []tool.Tool { return nil }
func (a *cancellationIgnoringAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	// Capture the delay before spawning the goroutine: the test restores the
	// timeout variable when it finishes, which would race a read inside the
	// goroutine.
	delay := cancelledAgentStreamCloseTimeout / 2
	go func() {
		// Write the agent name after the runner starts draining, without any
		// synchronization, so a completion callback reading the live
		// invocation would race this write under -race.
		time.Sleep(delay)
		inv.AgentName = "late-agent"
	}()
	return ch, nil
}

// TestRunnerCompletionCancelledBeforeFirstEventNoRace guards against a data
// race between the agent goroutine's asynchronous invocation setup (which
// writes Invocation.AgentName via setupInvocation) and the runner completion
// cleanup. When the context is cancelled before the first agent event arrives,
// runEventLoop returns via ctx.Done() with no channel happens-before edge
// against the agent goroutine, so the cleanup must not read the live
// invocation. Run with -race; the previous implementation raced inside
// agent.EmitEvent's read of inv.AgentName, and a second window remained
// reachable through plugin callbacks (the built-in debug-log event hook reads
// Invocation.AgentName when an event is emitted).
func TestRunnerCompletionCancelledBeforeFirstEventNoRace(t *testing.T) {
	runCancelled := func(t *testing.T, opts ...Option) {
		t.Helper()
		ag := chainagent.New("chain")
		successful := 0
		for i := 0; i < 100; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r := NewRunner("app", ag, opts...)
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

	t.Run("default plugins", func(t *testing.T) {
		runCancelled(t)
	})
	t.Run("debuglog event hook", func(t *testing.T) {
		// The built-in debug-log event hook reads Invocation.AgentName when
		// an event is emitted, exercising the plugin-interface path of the
		// pre-first-event cancellation race.
		runCancelled(t, WithPlugins(debuglog.New(debuglog.WithEventEnabled(true))))
	})
	t.Run("agent ignores cancellation", func(t *testing.T) {
		// Shrink the drain grace period so the timeout path is exercised.
		oldTimeout := cancelledAgentStreamCloseTimeout
		cancelledAgentStreamCloseTimeout = 50 * time.Millisecond
		defer func() { cancelledAgentStreamCloseTimeout = oldTimeout }()

		ag := &cancellationIgnoringAgent{name: "ignore-cancel"}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := NewRunner("app", ag, WithPlugins(debuglog.New(debuglog.WithEventEnabled(true))))
		ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("hi"))
		require.NoError(t, err)
		events := 0
		for range ch {
			events++
		}
		require.NotZero(t, events, "runner completion must still be emitted when the agent never closes its stream")
	})
}
