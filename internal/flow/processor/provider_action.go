//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"fmt"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ProviderActionResponseProcessor completes the provider-native actions that a
// completed model response delegates to the caller. It must run before the
// function call processor so action results precede ordinary tool results.
type ProviderActionResponseProcessor struct{}

const providerActionSourceExtension = "trpc.provider_action.source_event.v1"

type providerActionSourceKey struct{}

// MarkProviderActionSource records an identifier that survives Event.Clone.
func MarkProviderActionSource(evt *event.Event) {
	if evt == nil || evt.ID == "" {
		return
	}
	if source, ok, err := event.GetExtension[string](evt, providerActionSourceExtension); err == nil && ok && source != "" {
		return
	}
	_ = event.SetExtension(evt, providerActionSourceExtension, evt.ID)
}

// ProviderActionSource returns the stable source identifier of a response.
// Older events use their invocation and response IDs when available.
func ProviderActionSource(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	source, ok, err := event.GetExtension[string](evt, providerActionSourceExtension)
	if err == nil && ok && source != "" {
		return source
	}
	if evt.InvocationID != "" && evt.Response != nil && evt.Response.ID != "" {
		return "response:" + strconv.Quote(evt.InvocationID) + ":" + strconv.Quote(evt.Response.ID)
	}
	return evt.ID
}

// WithProviderActionSource associates action results with the response event
// that requested them. The source ID is carried through session persistence.
func WithProviderActionSource(ctx context.Context, eventID string) context.Context {
	if eventID == "" {
		return ctx
	}
	return context.WithValue(ctx, providerActionSourceKey{}, eventID)
}

// NewProviderActionResponseProcessor creates a provider action processor.
func NewProviderActionResponseProcessor() *ProviderActionResponseProcessor {
	return &ProviderActionResponseProcessor{}
}

// ProcessResponse implements flow.ResponseProcessor. Responses that do not
// request provider actions are left to the remaining processors. A response
// that requests them from a model without model.ProviderActionCompleter, such
// as a wrapper that does not forward the capability, ends the invocation with
// an error rather than replaying an unanswered action.
func (p *ProviderActionResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || invocation.Model == nil ||
		!providerdata.ResponseRequestsActions(rsp) {
		return
	}
	var messages []model.Message
	var err error
	var staged map[string]map[string]tool.Tool
	state := actionToolsForInvocation(invocation)
	if completer, ok := invocation.Model.(model.ProviderActionCompleter); ok {
		var actionCtx context.Context
		actionCtx, staged, err = state.collect(ctx, invocation, req)
		if err == nil {
			messages, err = completer.CompleteProviderActions(actionCtx, req, rsp)
		}
	} else {
		err = fmt.Errorf("model %q does not complete provider actions", invocation.Model.Info().Name)
	}
	if err == nil {
		err = validateProviderActionResults(messages, invocation.Model.Info())
	}
	if err != nil {
		invocation.EndInvocation = true
		if ctx.Err() != nil {
			return
		}
		log.ErrorfContext(
			ctx,
			"Provider action handling failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		if err := agent.EmitEvent(ctx, invocation, ch, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			fmt.Sprintf("complete provider actions: %v", err),
		)); err != nil {
			log.DebugContext(
				ctx,
				"Provider action response processor: context cancelled",
			)
		}
		return
	}
	for _, message := range messages {
		resultEvent := newToolCallResponseEvent(
			invocation,
			rsp,
			[]model.Choice{{Index: 0, Message: message}},
		)
		if sourceID, ok := ctx.Value(providerActionSourceKey{}).(string); ok && sourceID != "" {
			_ = event.SetExtension(resultEvent, providerActionSourceExtension, sourceID)
		}
		bindings := staged[message.ToolID]
		recordActionToolKeys(resultEvent, bindings)
		if _, err := emitFunctionResponseEventAndWait(
			ctx,
			invocation,
			ch,
			resultEvent,
		); err != nil {
			invocation.EndInvocation = true
			return
		}
		if len(bindings) > 0 {
			state.live[message.ToolID] = bindings
		}
		if rsp.IsToolCallResponse() {
			if state.response != rsp {
				state.response = rsp
				state.justCompleted = nil
			}
			state.justCompleted = append(state.justCompleted, message)
		}
	}
}

func validateProviderActionResults(messages []model.Message, info model.Info) error {
	for index, message := range messages {
		if message.Role != model.RoleTool || message.ToolID == "" || message.ProviderData == nil {
			return fmt.Errorf("provider action result %d must be a tool message with a tool id and provider data", index)
		}
		if err := providerdata.ValidateMessage(message, info); err != nil {
			return fmt.Errorf("provider action result %d: %w", index, err)
		}
	}
	return nil
}
