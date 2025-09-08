//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// functionCallResponseProcessor executes function/tool calls when present in LLM responses.
// It runs within the unified response processors pipeline.
type functionCallResponseProcessor struct{ f *Flow }

func newFunctionCallResponseProcessor(f *Flow) flow.ResponseProcessor {
	return &functionCallResponseProcessor{f: f}
}

// NewFunctionCallResponseProcessor exposes a constructor for the function-call
// response processor so callers can compose processor order explicitly.
func NewFunctionCallResponseProcessor(f *Flow) flow.ResponseProcessor {
	return newFunctionCallResponseProcessor(f)
}

func (p *functionCallResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if rsp == nil || len(rsp.Choices) == 0 || len(rsp.Choices[0].Message.ToolCalls) == 0 {
		return
	}

	// Build tools map from the agent at response-time (matches request-time tools).
	tools := make(map[string]tool.Tool)
	if invocation.Agent != nil {
		for _, t := range invocation.Agent.Tools() {
			tools[t.Declaration().Name] = t
		}
	}

	// Create an LLM response event to carry metadata for tool response construction.
	llmEvent := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithBranch(invocation.Branch),
		event.WithResponse(rsp),
	)

	// Handle calls and emit function response event.
	functionResponseEvent, err := p.f.handleFunctionCallsAndSendEvent(ctx, invocation, llmEvent, tools, ch)
	if err != nil {
		// Errors are already emitted by handleFunctionCallsAndSendEvent.
		return
	}
	if functionResponseEvent != nil {
		_ = p.f.waitForCompletion(ctx, invocation, functionResponseEvent)
	}
}
