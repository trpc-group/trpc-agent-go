//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package finalization

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestCompetingAppendsShareAcknowledgement(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "uncertain"}[fail], func(t *testing.T) {
			var writes atomic.Int32
			injected := errors.New("store may have committed")
			inv := agent.NewInvocation()
			c := Attach(inv, func(context.Context, *event.Event) error {
				writes.Add(1)
				if fail {
					return injected
				}
				return nil
			})
			e := event.New("inv", "agent")
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := c.Append(context.Background(), e)
					if fail {
						require.ErrorIs(t, err, injected)
					} else {
						require.NoError(t, err)
					}
				}()
			}
			wg.Wait()
			require.EqualValues(t, 1, writes.Load(), "an ambiguous write must not be retried")
			err, ok := c.Result(e.ID)
			require.True(t, ok)
			if fail {
				require.ErrorIs(t, err, injected)
				require.ErrorIs(t, c.Err(), injected)
			}
			require.Error(t, Acknowledged(inv, "unconsumed"))
		})
	}
}

func TestAppendWaitCanBeCancelled(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := Attach(agent.NewInvocation(), func(context.Context, *event.Event) error { close(entered); <-release; return nil })
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Append(context.Background(), event.New("i", "a")) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.Append(ctx, event.New("i", "a")), context.Canceled)
	close(release)
	<-done
}
