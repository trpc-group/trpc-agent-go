//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCompactionProgressOutcomesAndAttempts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service session.Service
		want    event.ProgressStatus
	}{
		{"summary created", &summaryInjectingService{}, event.ProgressStatusCompleted},
		{"no summary update", &summaryFailingService{}, event.ProgressStatusSkipped},
		{"summary failure", &summaryFailingService{err: errors.New("private details")}, event.ProgressStatusFailed},
		{"partial failure", &summaryPartialFailureService{err: errors.New("private details")}, event.ProgressStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := agent.NewInvocation(agent.WithInvocationSession(&session.Session{}),
				agent.WithInvocationSessionService(tc.service),
				agent.WithInvocationRunOptions(agent.RunOptions{ProgressEventsEnabled: true}))
			var previousID string
			for attempt := 0; attempt < 2; attempt++ {
				ch := make(chan *event.Event, 2)
				req := &model.Request{}
				result := new(Flow).runContextCompaction(context.Background(), inv, ch, req,
					&contextCompactionRebuildPlan{}, contextCompactionDecision{shouldCompact: true})
				require.Same(t, req, result, "no rebuild is available in this fixture")
				require.Len(t, ch, 2)
				start, ok, err := event.GetExtension[event.Progress](<-ch, event.ProgressExtensionKey)
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, event.ProgressStatusStarted, start.Status)
				require.NotEmpty(t, start.OperationID)
				require.NotEqual(t, previousID, start.OperationID)
				previousID = start.OperationID
				terminalEvent := <-ch
				require.Nil(t, terminalEvent.Error, "a recoverable summary failure is not a run failure")
				terminal, ok, err := event.GetExtension[event.Progress](terminalEvent, event.ProgressExtensionKey)
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, start.OperationID, terminal.OperationID)
				require.Equal(t, tc.want, terminal.Status)
				require.Nil(t, terminal.Percent)
				require.NotContains(t, terminal.Message, "private details")
			}
		})
	}
}

type cancelledProgressSummaryService struct{ session.Service }

func (*cancelledProgressSummaryService) CreateSessionSummary(ctx context.Context, _ *session.Session, _ string, _ bool) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestCompactionProgressCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inv := agent.NewInvocation(agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationSessionService(&cancelledProgressSummaryService{}),
		agent.WithInvocationRunOptions(agent.RunOptions{ProgressEventsEnabled: true}))
	ch := make(chan *event.Event, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		new(Flow).runContextCompaction(ctx, inv, ch, &model.Request{},
			&contextCompactionRebuildPlan{}, contextCompactionDecision{shouldCompact: true})
	}()
	select {
	case e := <-ch:
		p, ok, err := event.GetExtension[event.Progress](e, event.ProgressExtensionKey)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, event.ProgressStatusStarted, p.Status)
	case <-time.After(5 * time.Second):
		t.Fatal("missing start notification")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled progress blocked the run")
	}
	// Delivery after cancellation is best effort. If delivered, it must not
	// claim success or turn the recoverable operation into a model error.
	select {
	case e := <-ch:
		p, ok, err := event.GetExtension[event.Progress](e, event.ProgressExtensionKey)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, event.ProgressStatusCancelled, p.Status)
		require.Nil(t, e.Error)
	default:
	}
}
