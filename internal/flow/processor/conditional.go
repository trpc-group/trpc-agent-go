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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// InvocationPredicate decides whether one processor should run for the current invocation.
type InvocationPredicate func(context.Context, *agent.Invocation) bool

// ConditionalRequestProcessor runs one request processor only when the predicate passes.
type ConditionalRequestProcessor struct {
	predicate InvocationPredicate
	delegate  flow.RequestProcessor
}

// NewConditionalRequestProcessor creates a request processor guarded by one invocation predicate.
func NewConditionalRequestProcessor(
	predicate InvocationPredicate,
	delegate flow.RequestProcessor,
) *ConditionalRequestProcessor {
	return &ConditionalRequestProcessor{
		predicate: predicate,
		delegate:  delegate,
	}
}

// ProcessRequest runs the wrapped request processor only when enabled for this invocation.
func (p *ConditionalRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if p == nil || p.delegate == nil {
		return
	}
	if p.predicate != nil && !p.predicate(ctx, invocation) {
		return
	}
	p.delegate.ProcessRequest(ctx, invocation, req, ch)
}

// ConditionalResponseProcessor runs one response processor only when the predicate passes.
type ConditionalResponseProcessor struct {
	predicate InvocationPredicate
	delegate  flow.ResponseProcessor
}

// NewConditionalResponseProcessor creates a response processor guarded by one invocation predicate.
func NewConditionalResponseProcessor(
	predicate InvocationPredicate,
	delegate flow.ResponseProcessor,
) *ConditionalResponseProcessor {
	return &ConditionalResponseProcessor{
		predicate: predicate,
		delegate:  delegate,
	}
}

// ProcessResponse runs the wrapped response processor only when enabled for this invocation.
func (p *ConditionalResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if p == nil || p.delegate == nil {
		return
	}
	if p.predicate != nil && !p.predicate(ctx, invocation) {
		return
	}
	p.delegate.ProcessResponse(ctx, invocation, req, rsp, ch)
}
