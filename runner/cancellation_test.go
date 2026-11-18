//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const cancellationOutputCloseTimeout = 5 * time.Second

func waitForCancellationOutputClose(t *testing.T, events <-chan *event.Event, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatalf("runner output did not close within %s", timeout)
		}
	}
}

func cancelModelStreamOnPartial(t *testing.T, events <-chan *event.Event, cancel context.CancelFunc, firstPartialTimeout time.Duration) time.Duration {
	t.Helper()
	timer := time.NewTimer(firstPartialTimeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-events:
			require.True(t, ok, "model must emit a partial response before output closes")
			if ev.Response == nil || !ev.IsPartial {
				continue
			}
			cancelledAt := time.Now()
			cancel()
			waitForCancellationOutputClose(t, events, cancellationOutputCloseTimeout)
			return time.Since(cancelledAt)
		case <-timer.C:
			t.Fatalf("model did not emit a partial response within %s", firstPartialTimeout)
		}
	}
}

type cancellationOnlyStreamModel struct {
	stopped chan struct{}
}

func (*cancellationOnlyStreamModel) Info() model.Info {
	return model.Info{Name: "cancellation-only"}
}

func (m *cancellationOnlyStreamModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	responses := make(chan *model.Response)
	go func() {
		defer close(m.stopped)
		defer close(responses)
		select {
		case responses <- &model.Response{
			IsPartial: true,
			Choices:   []model.Choice{{Delta: model.NewAssistantMessage("partial")}},
		}:
		case <-ctx.Done():
			return
		}
		// Natural completion cannot make the cancellation assertion pass.
		<-ctx.Done()
	}()
	return responses, nil
}

func TestCancellation_ModelStreamClosesOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancellationOnlyStreamModel{stopped: make(chan struct{})}
	r := runner.NewRunner("cancel-stream", llmagent.New("agent", llmagent.WithModel(m),
		llmagent.WithMaxLLMCalls(1),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true})))
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	events, err := r.Run(ctx, "user", "stream", model.NewUserMessage("hello"))
	require.NoError(t, err)
	cancelModelStreamOnPartial(t, events, cancel, 5*time.Second)
	timer := time.NewTimer(cancellationOutputCloseTimeout)
	defer timer.Stop()
	select {
	case <-m.stopped:
	case <-timer.C:
		t.Fatal("model did not observe cancellation")
	}
}
