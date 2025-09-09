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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// FunctionCallHandler executes tool calls for an LLM response and optionally
// returns a tool response event that may require completion before continuing.
type FunctionCallHandler func(
	ctx context.Context,
	invocation *agent.Invocation,
	llmEvent *event.Event,
	tools map[string]tool.Tool,
	ch chan<- *event.Event,
) (*event.Event, error)

// WaitForCompletionFunc waits for the provided event to complete if required.
type WaitForCompletionFunc func(
	ctx context.Context,
	invocation *agent.Invocation,
	lastEvent *event.Event,
) error

// FunctionCallResponseProcessor executes function/tool calls when present in LLM responses.
// The concrete execution logic is injected via FunctionCallHandler and WaitForCompletionFunc
// to avoid package cycles with llmflow.
type FunctionCallResponseProcessor struct {
	handle FunctionCallHandler
	wait   WaitForCompletionFunc
}

// NewFunctionCallResponseProcessor creates a response processor for function/tool calls.
func NewFunctionCallResponseProcessor(handle FunctionCallHandler, wait WaitForCompletionFunc) flow.ResponseProcessor {
	return &FunctionCallResponseProcessor{handle: handle, wait: wait}
}

// ProcessResponse implements the flow.ResponseProcessor interface.
func (p *FunctionCallResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if rsp == nil || len(rsp.Choices) == 0 || len(rsp.Choices[0].Message.ToolCalls) == 0 {
		return
	}

	// Prefer tools from the concrete request (supports dynamic tool filtering),
	// which were prepared during preprocess and possibly modified by callbacks.
	tools := make(map[string]tool.Tool)
	if req != nil && req.Tools != nil {
		for name, t := range req.Tools {
			tools[name] = t
		}
	}

	// Create an LLM response event to carry metadata for tool response construction.
	llmEvent := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithBranch(invocation.Branch),
		event.WithResponse(rsp),
	)

	// Handle calls and emit function response event.
	functionResponseEvent, err := p.handle(ctx, invocation, llmEvent, tools, ch)
	if err != nil {
		// Errors are emitted by the handler.
		return
	}
	if functionResponseEvent != nil && p.wait != nil {
		_ = p.wait(ctx, invocation, functionResponseEvent)
	}
}
