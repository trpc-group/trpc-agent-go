//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func pluginAgentCallbacks(inv *Invocation) *Callbacks {
	if inv == nil || inv.Plugins == nil {
		return nil
	}
	return inv.Plugins.AgentCallbacks()
}

// RunWithPlugins runs an agent with Runner-provided plugins applied.
//
// This wrapper is used by the Runner and by internal multi-agent helpers
// (e.g., chain, parallel, cycle, transfer, graph agent-nodes) so plugins
// consistently apply even when agents are invoked indirectly.
//
// Callback semantics (what callers should know):
//
//  1. BeforeAgent fires ONCE per invocation passed in. For multi-agent
//     containers (chain, parallel, cycle, graph agent-node), the callback
//     fires once for EACH sub-agent invocation — not just once for the root
//     run. Hooks that expect "one call per Runner turn" must check
//     `args.Invocation.Agent` or similar to distinguish.
//
//  2. BeforeAgent.CustomResponse SHORT-CIRCUITS the sub-agent: `ag.Run` is
//     not called and no other events are produced besides the synthetic
//     response event. Code paths that rely on the sub-agent emitting
//     terminal state (for example, graph's `GraphCompletionEvent` used by
//     agent-nodes to populate `SubgraphResult.FinalState`) will NOT see
//     those events — callers / output mappers must handle a nil
//     `FinalState` gracefully when short-circuit is possible.
//
//  3. BeforeAgent may return a derived Context; both `ag.Run` and the
//     background AfterAgent goroutine observe it via `CloneContext`.
//
//  4. AfterAgent fires after the sub-agent's event stream closes and
//     receives the last non-partial response event in `args.FullResponseEvent`
//     (nil if there wasn't one). If `args.Error` is non-nil, it was
//     derived from the sub-agent's final `model.ResponseError`.
//
//  5. AfterAgent.CustomResponse APPENDS an extra response event to the
//     forwarded stream. In consumers that track "last response" (the graph
//     agent-node's `StateKeyLastResponse` and analogous downstream state),
//     this appended event becomes the new last response. Use this
//     intentionally when you want to override the sub-agent's output.
//
//  6. AfterAgent returning an error appends a single error event of type
//     `ErrorTypeAgentCallbackError` instead of overriding the response.
func RunWithPlugins(
	ctx context.Context,
	invocation *Invocation,
	ag Agent,
) (<-chan *event.Event, error) {
	if ag == nil {
		return nil, errors.New("agent is nil")
	}
	callbacks := pluginAgentCallbacks(invocation)
	if callbacks == nil {
		return ag.Run(ctx, invocation)
	}

	result, err := callbacks.RunBeforeAgent(
		ctx,
		&BeforeAgentArgs{Invocation: invocation},
	)
	if err != nil {
		return nil, err
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return singleResponseEventChan(ctx, invocation, result.CustomResponse),
			nil
	}

	original, err := ag.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}
	return wrapAfterAgentCallbacks(ctx, invocation, callbacks, original), nil
}

func singleResponseEventChan(
	ctx context.Context,
	invocation *Invocation,
	rsp *model.Response,
) <-chan *event.Event {
	out := make(chan *event.Event, 1)
	runCtx := CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)

		evt := event.NewResponseEvent(
			invocationID(invocation),
			agentName(invocation),
			rsp,
		)
		_ = EmitEvent(ctx, invocation, out, evt)
	}(runCtx)
	return out
}

func invocationID(inv *Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.InvocationID
}

func agentName(inv *Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.AgentName
}

func wrapAfterAgentCallbacks(
	ctx context.Context,
	invocation *Invocation,
	callbacks *Callbacks,
	src <-chan *event.Event,
) <-chan *event.Event {
	out := make(chan *event.Event, cap(src))
	runCtx := CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)

		var fullRespEvent *event.Event
		for evt := range src {
			if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
				fullRespEvent = evt
			}
			if err := event.EmitEvent(ctx, out, evt); err != nil {
				return
			}
		}

		agentErr := agentErrorFromEvent(fullRespEvent)
		result, err := callbacks.RunAfterAgent(ctx, &AfterAgentArgs{
			Invocation:        invocation,
			FullResponseEvent: fullRespEvent,
			Error:             agentErr,
		})
		if result != nil && result.Context != nil {
			ctx = result.Context
		}

		evt := afterAgentEvent(invocation, result, err)
		if evt == nil {
			return
		}
		_ = EmitEvent(ctx, invocation, out, evt)
	}(runCtx)
	return out
}

func agentErrorFromEvent(fullRespEvent *event.Event) error {
	if fullRespEvent == nil || fullRespEvent.Response == nil {
		return nil
	}
	if fullRespEvent.Response.Error == nil {
		return nil
	}
	return fmt.Errorf(
		"%s: %s",
		fullRespEvent.Response.Error.Type,
		fullRespEvent.Response.Error.Message,
	)
}

func afterAgentEvent(
	invocation *Invocation,
	result *AfterAgentResult,
	callbackErr error,
) *event.Event {
	if callbackErr != nil {
		return event.NewErrorEvent(
			invocationID(invocation),
			agentName(invocation),
			ErrorTypeAgentCallbackError,
			callbackErr.Error(),
		)
	}
	if result == nil || result.CustomResponse == nil {
		return nil
	}
	return event.NewResponseEvent(
		invocationID(invocation),
		agentName(invocation),
		result.CustomResponse,
	)
}
