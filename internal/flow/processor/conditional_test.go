//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type conditionalRequestProcessorStub struct {
	called bool
}

func (p *conditionalRequestProcessorStub) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	p.called = true
}

// conditionalResponseProcessorStub is a test stub that records whether
// ProcessResponse was invoked.
type conditionalResponseProcessorStub struct {
	called bool
}

// ProcessResponse marks the stub as called and returns the original response.
func (p *conditionalResponseProcessorStub) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) *model.Response {
	p.called = true
	return rsp
}

func TestConditionalRequestProcessor_ProcessRequest(t *testing.T) {
	delegate := &conditionalRequestProcessorStub{}
	processor := NewConditionalRequestProcessor(
		func(ctx context.Context, inv *agent.Invocation) bool {
			_ = ctx
			return inv != nil && inv.InvocationID == "allowed"
		},
		delegate,
	)

	processor.ProcessRequest(
		context.Background(),
		&agent.Invocation{InvocationID: "blocked"},
		&model.Request{},
		make(chan *event.Event, 1),
	)
	if delegate.called {
		t.Fatalf("expected delegate to be skipped when predicate blocks invocation")
	}

	processor.ProcessRequest(
		context.Background(),
		&agent.Invocation{InvocationID: "allowed"},
		&model.Request{},
		make(chan *event.Event, 1),
	)
	if !delegate.called {
		t.Fatalf("expected delegate to run when predicate allows invocation")
	}
}

func TestConditionalResponseProcessor_ProcessResponse(t *testing.T) {
	delegate := &conditionalResponseProcessorStub{}
	processor := NewConditionalResponseProcessor(
		func(ctx context.Context, inv *agent.Invocation) bool {
			_ = ctx
			return inv != nil && inv.InvocationID == "allowed"
		},
		delegate,
	)

	processor.ProcessResponse(
		context.Background(),
		&agent.Invocation{InvocationID: "blocked"},
		&model.Request{},
		&model.Response{},
		make(chan *event.Event, 1),
	)
	if delegate.called {
		t.Fatalf("expected delegate to be skipped when predicate blocks invocation")
	}

	processor.ProcessResponse(
		context.Background(),
		&agent.Invocation{InvocationID: "allowed"},
		&model.Request{},
		&model.Response{},
		make(chan *event.Event, 1),
	)
	if !delegate.called {
		t.Fatalf("expected delegate to run when predicate allows invocation")
	}
}

func TestConditionalResponseProcessor_NilDelegateReturnsOriginal(t *testing.T) {
	proc := &ConditionalResponseProcessor{}
	rsp := &model.Response{}
	got := proc.ProcessResponse(
		context.Background(),
		&agent.Invocation{},
		&model.Request{},
		rsp,
		make(chan *event.Event, 1),
	)
	if got != rsp {
		t.Fatalf("expected original response to be returned, got %v", got)
	}
}
