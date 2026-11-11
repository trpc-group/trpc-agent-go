//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageorigin

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type inheritedInputsKey struct{}

// InheritedUserInputs returns read-only input snapshots captured when an agent
// delegated work to an independent runner. They are permission evidence only,
// not additional model messages or a live view of the parent's session.
func InheritedUserInputs(ctx context.Context) []event.Event {
	inputs, _ := ctx.Value(inheritedInputsKey{}).([]event.Event)
	return inputs
}

// VisibleUserInput reports whether an attributable host input is visible to
// the invocation or one of its ancestors, including isolated child histories.
func VisibleUserInput(evt *event.Event, inv *agent.Invocation) bool {
	if InputSource(evt) != UserInput {
		return false
	}
	for current := inv; current != nil; current = current.GetParentInvocation() {
		if evt.Filter(current.GetEventFilterKey()) {
			return true
		}
	}
	return false
}

// CaptureDelegation snapshots visible user inputs for an independent child
// runner. The returned decorator marks its input as agent-authored and retains
// no parent context, invocation, session, or cancellation lifetime. Text is
// copied; non-text parts retain only their type so omission remains observable.
// Previously inherited inputs precede this session's inputs for nested tasks.
func CaptureDelegation(ctx context.Context) (func(context.Context) context.Context, error) {
	inputs := append([]event.Event(nil), InheritedUserInputs(ctx)...)
	inv, _ := agent.InvocationFromContext(ctx)
	if inv != nil {
		if err := flush.Invoke(ctx, inv); err != nil {
			return nil, err
		}
		sess := inv.Session
		if live, ok := livesession.Get(inv); ok {
			sess = live
		}
		if sess != nil {
			sess.EventMu.RLock()
			for i := range sess.Events {
				evt := &sess.Events[i]
				if evt.Response != nil && !evt.IsPartial && VisibleUserInput(evt, inv) {
					inputs = append(inputs, snapshotUserInput(evt))
				}
			}
			sess.EventMu.RUnlock()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func(child context.Context) context.Context {
		return context.WithValue(WithAgentInput(child), inheritedInputsKey{}, inputs)
	}, nil
}

func snapshotUserInput(evt *event.Event) event.Event {
	copy := event.Event{
		ID: evt.ID, InvocationID: evt.InvocationID, ParentInvocationID: evt.ParentInvocationID,
		Response: &model.Response{},
	}
	for _, choice := range evt.Choices {
		if choice.Message.Role != model.RoleUser {
			continue
		}
		message := model.Message{Role: model.RoleUser, Content: choice.Message.Content}
		for _, part := range choice.Message.ContentParts {
			cloned := model.ContentPart{Type: part.Type}
			if part.Type == model.ContentTypeText && part.Text != nil {
				text := *part.Text
				cloned.Text = &text
			}
			message.ContentParts = append(message.ContentParts, cloned)
		}
		copy.Choices = append(copy.Choices, model.Choice{Message: message})
	}
	MarkInput(&copy, UserInput)
	return copy
}
