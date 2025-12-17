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
	"fmt"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/reduce"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MessagesSnapshotProvider provides a MessagesSnapshot event stream by replaying persisted AG-UI track events.
type MessagesSnapshotProvider interface {
	// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
	MessagesSnapshot(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
func (r *runner) MessagesSnapshot(ctx context.Context,
	runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	if r.appName == "" {
		return nil, errors.New("agui: app name is empty")
	}
	if r.tracker == nil {
		return nil, errors.New("agui: tracker is nil")
	}
	runAgentInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("agui: run input hook: %w", err)
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve user ID: %w", err)
	}
	input := &runInput{
		key: session.Key{
			AppName:   r.appName,
			UserID:    userID,
			SessionID: runAgentInput.ThreadID,
		},
		threadID:    runAgentInput.ThreadID,
		runID:       runAgentInput.RunID,
		userID:      userID,
		enableTrack: false,
	}
	events := make(chan aguievents.Event)
	runCtx := agent.CloneContext(ctx)
	go r.messagesSnapshot(runCtx, input, events)
	return events, nil
}

// messagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
func (r *runner) messagesSnapshot(ctx context.Context, input *runInput, events chan<- aguievents.Event) {
	defer close(events)
	threadID := input.threadID
	runID := input.runID
	// Emit a RUN_STARTED event to anchor the synthetic run.
	if !r.emitEvent(ctx, events, aguievents.NewRunStartedEvent(threadID, runID), input) {
		return
	}

	messagesSnapshotEvent, err := r.getMessagesSnapshotEvent(ctx, input.key)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui messages snapshot: threadID: %s, runID: %s, "+
				"load history: %v",
			threadID,
			runID,
			err,
		)
		if messagesSnapshotEvent == nil {
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("load history: %v", err),
				aguievents.WithRunID(runID)), input)
			return
		}
	}
	// In order to fetch the history messages as much as possible, still emit the messages even if there is an error.
	// Emit a MESSAGES_SNAPSHOT event to send the snapshot payload.
	if !r.emitEvent(ctx, events, messagesSnapshotEvent, input) {
		return
	}
	if err != nil {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("load history: %v", err),
			aguievents.WithRunID(runID)), input)
		return
	}
	// Emit a RUN_FINISHED event to signal downstream consumers there is no more data.
	if !r.emitEvent(ctx, events, aguievents.NewRunFinishedEvent(threadID, runID), input) {
		return
	}
}

// getMessagesSnapshotEvent loads AG-UI track events and converts them to an AG-UI MessagesSnapshotEvent.
// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
func (r *runner) getMessagesSnapshotEvent(ctx context.Context,
	sessionKey session.Key) (*aguievents.MessagesSnapshotEvent, error) {
	trackEvents, err := r.tracker.GetEvents(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	messages, err := reduce.Reduce(r.appName, sessionKey.UserID, trackEvents.Events)
	if err != nil {
		err = fmt.Errorf("reduce track events: %w", err)
	}
	return aguievents.NewMessagesSnapshotEvent(messages), err
}
