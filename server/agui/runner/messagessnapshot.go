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
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/reduce"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MessagesSnapshotter provides a MessagesSnapshot event stream by replaying persisted AG-UI track events.
type MessagesSnapshotter interface {
	// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
	MessagesSnapshot(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
func (r *runner) MessagesSnapshot(ctx context.Context,
	runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("run input cannot be nil")
	}
	if r.appName == "" {
		return nil, errors.New("app name is empty")
	}
	if r.tracker == nil {
		return nil, errors.New("tracker is nil")
	}
	runAgentInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("run input hook: %w", err)
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

	messagesSnapshotEvent, trackEvents, err := r.getMessagesSnapshotEvent(ctx, input.key)
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

	if r.messagesSnapshotFollowEnabled && trackEvents != nil && !trackEndsWithTerminalRunEvent(trackEvents.Events) {
		r.messagesSnapshotFollow(ctx, input, events, trackEvents)
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
	sessionKey session.Key) (*aguievents.MessagesSnapshotEvent, *session.TrackEvents, error) {
	trackEvents, err := r.tracker.GetEvents(ctx, sessionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("get track events: %w", err)
	}
	messages, err := reduce.Reduce(r.appName, sessionKey.UserID, trackEvents.Events)
	if err != nil {
		err = fmt.Errorf("reduce track events: %w", err)
	}
	return aguievents.NewMessagesSnapshotEvent(messages), trackEvents, err
}

func (r *runner) messagesSnapshotFollow(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	initial *session.TrackEvents,
) {
	cursorTime := lastTrackTimestamp(initial)
	pollInterval := r.flushInterval
	if pollInterval <= 0 {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("messages snapshot follow requires a positive flush interval",
			aguievents.WithRunID(input.runID)), input)
		return
	}
	maxDuration := r.messagesSnapshotFollowMaxDuration
	if maxDuration <= 0 {
		maxDuration = r.timeout
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var timer *time.Timer
	var timeout <-chan time.Time
	if maxDuration > 0 {
		timer = time.NewTimer(maxDuration)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("messages snapshot follow timeout",
				aguievents.WithRunID(input.runID)), input)
			return
		case <-ticker.C:
			if !r.handleMessagesSnapshotFollowTick(ctx, input, events, &cursorTime) {
				return
			}
		}
	}
}

func (r *runner) handleMessagesSnapshotFollowTick(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	cursorTime *time.Time,
) bool {
	trackEvents, err := r.tracker.GetEvents(ctx, input.key, session.WithEventTime(*cursorTime))
	if err != nil {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("follow track events: %v", err),
			aguievents.WithRunID(input.runID)), input)
		return false
	}
	if trackEvents == nil || len(trackEvents.Events) == 0 {
		return true
	}
	for _, trackEvent := range trackEvents.Events {
		if !trackEvent.Timestamp.After(*cursorTime) {
			continue
		}
		*cursorTime = trackEvent.Timestamp
		if len(trackEvent.Payload) == 0 {
			continue
		}
		evt, err := aguievents.EventFromJSON(trackEvent.Payload)
		if err != nil {
			log.WarnfContext(ctx, "agui messages snapshot follow: decode track event: %v", err)
			continue
		}
		terminal, terminalErr := terminalRunSignal(evt)
		if terminal {
			if terminalErr != "" {
				r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(terminalErr,
					aguievents.WithRunID(input.runID)), input)
				return false
			}
			r.emitEvent(ctx, events, aguievents.NewRunFinishedEvent(input.threadID, input.runID), input)
			return false
		}
		if !r.emitEvent(ctx, events, evt, input) {
			return false
		}
	}
	return true
}

func trackEndsWithTerminalRunEvent(events []session.TrackEvent) bool {
	if len(events) == 0 {
		return false
	}
	last := events[len(events)-1]
	if len(last.Payload) == 0 {
		return false
	}
	evt, err := aguievents.EventFromJSON(last.Payload)
	if err != nil {
		return false
	}
	terminal, _ := terminalRunSignal(evt)
	return terminal
}

func terminalRunSignal(evt aguievents.Event) (terminal bool, errMessage string) {
	switch e := evt.(type) {
	case *aguievents.RunFinishedEvent:
		return true, ""
	case *aguievents.RunErrorEvent:
		return true, e.Message
	default:
		return false, ""
	}
}

func lastTrackTimestamp(trackEvents *session.TrackEvents) time.Time {
	if trackEvents == nil || len(trackEvents.Events) == 0 {
		return time.Time{}
	}
	return trackEvents.Events[len(trackEvents.Events)-1].Timestamp
}
