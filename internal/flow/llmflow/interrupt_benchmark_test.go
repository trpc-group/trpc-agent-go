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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type streamLookupKey struct{}
type streamLookupContext struct {
	context.Context
	lookups int
}

func (c *streamLookupContext) Value(key any) any {
	if _, ok := key.(streamLookupKey); ok {
		c.lookups++
	}
	return c.Context.Value(key)
}

func TestInterruptStreamContextLookupsStayLinear(t *testing.T) {
	const chunks = 4000
	ctx := &streamLookupContext{Context: context.Background()}
	inv := agent.NewInvocation()
	steer.Attach(inv, steer.NewQueue())
	steer.EnableInterrupt(inv)
	modelCtx, cycle, err := beginModelCycle(ctx, inv)
	require.NoError(t, err)
	defer cycle.cancel()
	defer cycle.control.End()
	f := New(nil, nil, Options{})
	var processErr error
	p := newStreamingResponseProcessor(f, responseContext(modelCtx, cycle), inv, inv, &model.Request{}, make(chan *event.Event, chunks), nil, false, &processErr)
	for i := 0; i < chunks; i++ {
		require.True(t, p.process(&model.Response{ID: "stream", IsPartial: true}))
		require.Nil(t, p.ctx.Value(streamLookupKey{}))
	}
	require.NoError(t, processErr)
	require.Equal(t, chunks, ctx.lookups, "a missing key must not traverse accumulated lifetime bridges")
}

func BenchmarkInterruptStream(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		for _, chunks := range []int{1000, 4000} {
			b.Run(fmt.Sprintf("enabled=%t/chunks=%d", enabled, chunks), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					ctx := context.Background()
					inv := agent.NewInvocation()
					if enabled {
						steer.Attach(inv, steer.NewQueue())
						steer.EnableInterrupt(inv)
					}
					modelCtx, cycle, err := beginModelCycle(ctx, inv)
					if err != nil {
						b.Fatal(err)
					}
					f := New(nil, nil, Options{})
					p := newStreamingResponseProcessor(f, responseContext(modelCtx, cycle), inv, inv, &model.Request{}, make(chan *event.Event, chunks), nil, false, &err)
					for n := 0; n < chunks; n++ {
						if !p.process(&model.Response{ID: "stream", IsPartial: true, Choices: []model.Choice{{Delta: model.NewAssistantMessage("x")}}}) {
							b.Fatal(err)
						}
					}
					if cycle != nil {
						cycle.cancel()
						cycle.control.End()
					}
				}
			})
		}
	}
}
