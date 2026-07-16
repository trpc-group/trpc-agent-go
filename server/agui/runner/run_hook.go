//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

var (
	errRunClosed       = errors.New("agui: run closed")
	errInvalidRunEvent = errors.New("agui: invalid run hook event")
)

// RunHook observes one AG-UI run and may emit run-scoped UI events.
type RunHook func(ctx context.Context, run *Run) error

// Run is the framework-created handle exposed to a RunHook.
type Run struct {
	input *adapter.RunAgentInput
	emit  chan<- hookEvent
	done  <-chan struct{}
}

type hookEvent struct {
	event aguievents.Event
	reply chan error
}

func newRun(input *adapter.RunAgentInput, emit chan<- hookEvent, done <-chan struct{}) *Run {
	return &Run{input: input, emit: emit, done: done}
}

// Input returns the request payload for this run.
func (r *Run) Input() *adapter.RunAgentInput {
	if r == nil || r.input == nil {
		return nil
	}
	return r.input
}

// Emit queues an AG-UI event to the same serialized stream as translated agent events.
func (r *Run) Emit(ctx context.Context, event aguievents.Event) error {
	if r == nil || r.emit == nil || r.done == nil {
		return errRunClosed
	}
	if err := validateRunHookEvent(event); err != nil {
		return err
	}
	req := hookEvent{event: event, reply: make(chan error, 1)}
	select {
	case <-r.done:
		return errRunClosed
	case <-ctx.Done():
		return ctx.Err()
	case r.emit <- req:
	}
	select {
	case err := <-req.reply:
		return err
	default:
	}
	select {
	case err := <-req.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		select {
		case err := <-req.reply:
			return err
		default:
			return errRunClosed
		}
	}
}

func validateRunHookEvent(event aguievents.Event) error {
	if event == nil {
		return errInvalidRunEvent
	}
	switch event.Type() {
	case aguievents.EventTypeRunStarted,
		aguievents.EventTypeRunFinished,
		aguievents.EventTypeRunError,
		aguievents.EventTypeMessagesSnapshot:
		return errInvalidRunEvent
	default:
		return nil
	}
}
