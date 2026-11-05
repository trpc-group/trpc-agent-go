//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner provides the core runner functionality.
package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionroute"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
)

// finishSteeredAssistant settles only the interrupted lineage before new input
// can be consumed. Clearing the accumulator also prevents root cancellation
// from resurrecting a previous generation's partial text.
func (r *runner) finishSteeredAssistant(ctx context.Context, loop *eventLoopContext, marker *event.Event) error {
	sess, _ := sessionroute.RouteEvent(loop.invocation, sessionroute.SnapshotEventIdentity(marker))
	acc := getInterruptedAssistantAccumulatorForEvent(loop, sess, marker)
	if acc == nil {
		return nil
	}
	defer func() {
		for key, candidate := range loop.interruptedAssistants {
			if candidate == acc {
				delete(loop.interruptedAssistants, key)
			}
		}
	}()
	if !shouldPersistInterruptedAssistant(loop) {
		return nil
	}
	evt := r.interruptedAssistantEventForAccumulator(ctx, loop, acc)
	if evt == nil {
		return nil
	}
	payload, err := json.Marshal(interruptedAssistantMetadata{Reason: "user_input"})
	if err != nil {
		return err
	}
	evt.Extensions = map[string]json.RawMessage{interruptedAssistantExtensionKey: payload}
	if err := r.processSingleAgentEvent(ctx, loop, evt); err != nil {
		return err
	}
	return steer.Err(loop.invocation)
}

// steerEventReceipt retains the pre-plugin requirements of an event. It is
// absent for default runs, whose notification and persistence rules are unchanged.
type steerEventReceipt struct {
	invocation       *agent.Invocation
	id               string
	needsPersistence bool
	required         bool
}

func (r *runner) beginSteeredEvent(ctx context.Context, loop *eventLoopContext, evt *event.Event) *steerEventReceipt {
	if !steer.Enabled(loop.invocation) {
		return nil
	}
	receipt := &steerEventReceipt{invocation: loop.invocation, id: evt.ID, needsPersistence: r.shouldPersistEvent(evt)}
	receipt.required = evt.RequiresCompletion || receipt.needsPersistence
	if evt.Response != nil && evt.Object == model.ObjectTypeModelInterrupted {
		if err := r.finishSteeredAssistant(ctx, loop, evt); err != nil {
			steer.Fail(loop.invocation, err)
			return receipt
		}
	}
	return receipt
}

func (r *steerEventReceipt) acknowledge() {
	if r != nil {
		steer.Acknowledge(r.invocation, r.id, steer.Err(r.invocation))
	}
}

func (r *steerEventReceipt) accept(evt *event.Event) bool {
	if evt == nil {
		if r != nil && r.required {
			steer.Fail(r.invocation, fmt.Errorf("required event filtered by plugin: %s", r.id))
		}
		return false
	}
	if r != nil && steer.Err(r.invocation) != nil {
		return evt.Response != nil && evt.Error != nil
	}
	return true
}

func (r *steerEventReceipt) persisted(ok bool) bool {
	if r == nil || !r.needsPersistence || ok {
		return true
	}
	steer.Fail(r.invocation, fmt.Errorf("required event was not persisted: %s", r.id))
	return false
}

func resetSteeredAccumulator(loop *eventLoopContext, acc *interruptedAssistantAccumulator, responseID string) {
	if steer.Enabled(loop.invocation) && acc.responseID != "" && acc.responseID != responseID {
		acc.choiceContent = nil
	}
}

func (r *steerEventReceipt) forwardable(allowed, persisted bool) bool {
	valid := r.persisted(persisted)
	return allowed && valid
}
