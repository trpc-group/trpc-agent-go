//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// parkedProducer is an agent.Agent whose event-production goroutine parks on a
// test-owned gate and ignores cancellation while parked. It records
// producer-done independently of the runner's consumer lifecycle, which is what
// lets a test observe whether the processed stream closing implies the producer
// has actually exited.
type parkedProducer struct {
	started      chan struct{} // closed once the producer goroutine begins
	hold         chan struct{} // producer parks here (ignoring ctx) until released
	producerDone atomic.Bool   // true only after the producer goroutine returns
	releaseOnce  sync.Once
}

func (p *parkedProducer) Run(_ context.Context, _ *agent.Invocation) (<-chan *event.Event, error) {
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		defer p.producerDone.Store(true)
		close(p.started)
		<-p.hold // a tool/model stream that outlives cancellation
	}()
	return out, nil
}

func (p *parkedProducer) Tools() []tool.Tool       { return nil }
func (p *parkedProducer) Info() agent.Info         { return agent.Info{Name: "parked-producer"} }
func (p *parkedProducer) SubAgents() []agent.Agent { return nil }
func (p *parkedProducer) FindSubAgent(string) agent.Agent {
	return nil
}
func (p *parkedProducer) release() { p.releaseOnce.Do(func() { close(p.hold) }) }

// nilChannelAgent is an agent.Agent whose Run returns a nil event channel and a
// nil error, mirroring a contract-violating custom agent. The runner's
// producer-done drain must not block forever on the nil channel; it must still
// close the processed stream so callers observe completion.
type nilChannelAgent struct{}

func (nilChannelAgent) Run(context.Context, *agent.Invocation) (<-chan *event.Event, error) {
	return nil, nil
}

func (nilChannelAgent) Tools() []tool.Tool       { return nil }
func (nilChannelAgent) Info() agent.Info         { return agent.Info{Name: "nil-channel"} }
func (nilChannelAgent) SubAgents() []agent.Agent { return nil }
func (nilChannelAgent) FindSubAgent(string) agent.Agent {
	return nil
}

// TestRun_NilAgentChannelDoesNotHang guards against the producer-done drain
// blocking on a nil agent channel: ranging a nil channel never returns, so
// without a nil check the processed stream would stay open forever after the
// loop returns on cancellation.
func TestRun_NilAgentChannelDoesNotHang(t *testing.T) {
	r := NewRunner(
		"nil-channel-app",
		nilChannelAgent{},
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer func() { _ = r.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := r.Run(ctx, "u", "s-nil-channel", model.NewUserMessage("go"))
	require.NoError(t, err, "the runner must accept the invocation")

	closed := make(chan struct{})
	go func() {
		for range out {
		}
		close(closed)
	}()

	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("processed stream never closed: the producer-done drain blocked on a nil channel")
	}
}

// TestRun_ProcessedCloseImpliesProducerDone pins the producer-done completion
// contract: the processed event stream closing must imply the agent's producer
// goroutine has exited. Before the fix, runEventLoop returned on ctx.Done and
// closed processedEventCh while the producer was still running (and cancelled
// the run handle only AFTER the close), so a consumer treating stream close as
// "done" would free resources under a live producer.
func TestRun_ProcessedCloseImpliesProducerDone(t *testing.T) {
	prod := &parkedProducer{
		started: make(chan struct{}),
		hold:    make(chan struct{}),
	}
	r := NewRunner(
		"producer-done-app",
		prod,
		WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer func() { _ = r.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out, err := r.Run(ctx, "u", "s-producer-done", model.NewUserMessage("go"))
	require.NoError(t, err, "the runner must accept the invocation")

	select {
	case <-prod.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the producer never started -- this test would measure nothing")
	}

	closed := make(chan struct{})
	go func() {
		for range out {
		}
		close(closed)
	}()

	// Cancel while the producer is parked (it ignores the context).
	cancel()

	// THE CONTRACT: while the producer is still parked, the processed stream
	// must NOT close. On the unfixed runner it closes within microseconds of
	// cancellation -- deterministic red.
	select {
	case <-closed:
		prod.release()
		t.Fatal("processed stream closed while the producer goroutine was still running -- " +
			"stream close must imply producer-done")
	case <-time.After(300 * time.Millisecond):
	}

	// Once the producer actually exits (released), the stream must close.
	prod.release()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("processed stream never closed after the producer exited")
	}
	require.True(t, prod.producerDone.Load(),
		"producer-done must hold at the moment the processed stream closes")
}
