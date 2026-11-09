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
	"runtime/debug"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/reduce"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MessagesSnapshotter provides a MessagesSnapshot event stream by replaying persisted AG-UI track events.
type MessagesSnapshotter interface {
	// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
	MessagesSnapshot(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// MessagesSnapshot sends a MessagesSnapshot event stream by replaying persisted AG-UI track events.
func (r *runner) MessagesSnapshot(ctx context.Context,
	runAgentInput *adapter.RunAgentInput) (eventCh <-chan aguievents.Event, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			// The error is written back to the HTTP client; keep the panic
			// payload (hooks/resolvers may embed request internals) in logs only.
			log.ErrorfContext(ctx, log.PanicPrefix+" agui messages snapshot: panic: %v\n%s", rec, debug.Stack())
			eventCh = nil
			err = errors.New("messages snapshot internal error")
		}
	}()
	if r.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("run input cannot be nil")
	}
	if r.tracker == nil {
		return nil, errors.New("tracker is nil")
	}
	runAgentInput, err = r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("run input hook: %w", err)
	}
	appName, err := r.resolveAppName(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve app name: %w", err)
	}
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve user ID: %w", err)
	}
	input := &runInput{
		key: session.Key{
			AppName:   appName,
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

	if r.messagesSnapshotFollowEnabled && r.shouldFollowMessagesSnapshot(input.key, trackEvents) {
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
	trackEvents, err := r.readSnapshotTrackEvents(ctx, sessionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("get track events: %w", err)
	}
	if trackEvents == nil {
		trackEvents = &session.TrackEvents{}
	}
	eventsForReduce, safeForFollow := trimTrackEventsToHistoryStart(trackEvents.Events)
	if !safeForFollow && len(trackEvents.Events) > 0 {
		log.WarnfContext(ctx, "agui messages snapshot: no safe user message boundary, app=%s, user=%s, session=%s, trackEvents=%d",
			sessionKey.AppName, sessionKey.UserID, sessionKey.SessionID, len(trackEvents.Events))
	}
	messages, err := reduce.Reduce(
		sessionKey.AppName,
		sessionKey.UserID,
		eventsForReduce,
		reduce.WithRunLifecycleEvents(r.messagesSnapshotRunLifecycleEventsEnabled),
		reduce.WithBestEffort(r.messagesSnapshotBestEffortEnabled),
	)
	if err != nil {
		err = fmt.Errorf("reduce track events: %w", err)
	}
	log.DebugfContext(ctx, "agui messages snapshot: app=%s, user=%s, session=%s, trackEvents=%d, eventsForReduce=%d, safeForFollow=%t, messages=%d, reduceErr=%v",
		sessionKey.AppName, sessionKey.UserID, sessionKey.SessionID, len(trackEvents.Events), len(eventsForReduce), safeForFollow, len(messages), err)
	event := aguievents.NewMessagesSnapshotEvent(messages)
	if r.eventSourceMetadataEnabled {
		metadata := source.BuildSnapshotMetadata(
			eventsForReduce,
			source.WithRunLifecycleEvents(r.messagesSnapshotRunLifecycleEventsEnabled),
		)
		if !metadata.IsZero() {
			event.GetBaseEvent().RawEvent = metadata
		}
	}
	if !safeForFollow {
		trackEvents = nil
	}
	return event, trackEvents, err
}

type cursorTrackReader interface {
	GetEventsWithCursor(context.Context, session.Key, string) (*session.TrackEvents, string, error)
}

func (r *runner) readSnapshotTrackEvents(ctx context.Context, key session.Key) (*session.TrackEvents, error) {
	reader, ok := r.tracker.(cursorTrackReader)
	if !ok || !r.messagesSnapshotFollowEnabled {
		return r.tracker.GetEvents(ctx, key)
	}
	events, _, err := reader.GetEventsWithCursor(ctx, key, "")
	if errors.Is(err, track.ErrCursorUnsupported) {
		return events, nil
	}
	return events, err
}

func trimTrackEventsToHistoryStart(events []session.TrackEvent) ([]session.TrackEvent, bool) {
	if len(events) == 0 {
		return events, true
	}
	for i, event := range events {
		if isUserMessageTrackEvent(event) {
			return events[i:], true
		}
	}
	return nil, false
}

func isUserMessageTrackEvent(trackEvent session.TrackEvent) bool {
	if len(trackEvent.Payload) == 0 {
		return false
	}
	evt, err := aguievents.EventFromJSON(trackEvent.Payload)
	if err != nil {
		return false
	}
	return isUserMessageEvent(evt)
}

func isUserMessageEvent(evt aguievents.Event) bool {
	switch e := evt.(type) {
	case *aguievents.CustomEvent:
		return e.Name == multimodal.CustomEventNameUserMessage
	case *aguievents.TextMessageStartEvent:
		return e.Role != nil && *e.Role == string(types.RoleUser)
	default:
		return false
	}
}

func (r *runner) messagesSnapshotFollow(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	initial *session.TrackEvents,
) {
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
	followCtx := ctx
	if maxDuration > 0 {
		var cancel context.CancelFunc
		followCtx, cancel = context.WithTimeout(ctx, maxDuration)
		defer cancel()
	}

	var read func(context.Context) bool
	if initial.Cursor == nil {
		timestamp := lastTrackTimestamp(initial)
		read = func(ctx context.Context) bool {
			return r.handleMessagesSnapshotFollowTick(ctx, input, events, &timestamp)
		}
	} else {
		reader, ok := r.tracker.(cursorTrackReader)
		if !ok || *initial.Cursor == "" {
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("follow track cursor unavailable",
				aguievents.WithRunID(input.runID)), input)
			return
		}
		cursor := *initial.Cursor
		read = func(ctx context.Context) bool {
			return r.drainCursorPages(ctx, input, events, reader, &cursor)
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for followCtx.Err() == nil {
		select {
		case <-followCtx.Done():
		case <-ticker.C:
			if followCtx.Err() == nil && !read(followCtx) {
				// A terminal event or error was already emitted unless the
				// follow context prevented delivery.
				if followCtx.Err() == nil {
					return
				}
			}
		}
	}
	if !input.terminalEmitted && ctx.Err() == nil && errors.Is(followCtx.Err(), context.DeadlineExceeded) {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("messages snapshot follow timeout",
			aguievents.WithRunID(input.runID)), input)
	}
}

func (r *runner) shouldFollowMessagesSnapshot(key session.Key, trackEvents *session.TrackEvents) bool {
	if trackEvents == nil || trackEndsWithTerminalRunEvent(trackEvents.Events) {
		return false
	}
	if r.flushInterval <= 0 {
		return false
	}
	if len(trackEvents.Events) > 0 {
		return true
	}
	return r.isRunning(key)
}

func (r *runner) handleMessagesSnapshotFollowTick(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	cursorTime *time.Time,
) bool {
	trackEvents, err := r.tracker.GetEvents(ctx, input.key, session.WithEventTime(*cursorTime))
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		if isEmptyTrackEventsError(err) {
			return true
		}
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("follow track events: %v", err),
			aguievents.WithRunID(input.runID)), input)
		return false
	}
	if trackEvents == nil || len(trackEvents.Events) == 0 {
		return true
	}
	return r.emitFollowTrackEvents(ctx, input, events, trackEvents.Events, cursorTime)
}

func (r *runner) drainCursorPages(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	reader cursorTrackReader,
	cursor *string,
) bool {
	for ctx.Err() == nil {
		page, next, err := reader.GetEventsWithCursor(ctx, input.key, *cursor)
		if ctx.Err() != nil {
			return false
		}
		if err == nil {
			switch {
			case page == nil || next == "":
				err = errors.New("follow track cursor unavailable")
			case len(page.Events) > 0 && next == *cursor:
				err = errors.New("follow track cursor did not advance")
			}
		}
		if err != nil {
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("follow track events: %v", err),
				aguievents.WithRunID(input.runID)), input)
			return false
		}
		if !r.emitFollowTrackEvents(ctx, input, events, page.Events, nil) {
			return false
		}
		if next == *cursor {
			return true
		}
		// Even an empty page can advance past filtered entries.
		*cursor = next
	}
	return false
}

func (r *runner) emitFollowTrackEvents(
	ctx context.Context,
	input *runInput,
	events chan<- aguievents.Event,
	trackEvents []session.TrackEvent,
	cursorTime *time.Time,
) bool {
	for _, trackEvent := range trackEvents {
		if ctx.Err() != nil {
			return false
		}
		if cursorTime != nil {
			if !trackEvent.Timestamp.After(*cursorTime) {
				continue
			}
			*cursorTime = trackEvent.Timestamp
		}
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

func isEmptyTrackEventsError(err error) bool {
	return errors.Is(err, session.ErrTracksEmpty) || errors.Is(err, session.ErrTrackEventsNotFound)
}

func trackEndsWithTerminalRunEvent(events []session.TrackEvent) bool {
	// Activity can follow a terminal event. Stop at a newer run boundary
	// before considering any terminal event from the preceding run.
	for i := len(events) - 1; i >= 0; i-- {
		evt, err := aguievents.EventFromJSON(events[i].Payload)
		if err != nil {
			continue
		}
		if _, started := evt.(*aguievents.RunStartedEvent); started || isUserMessageEvent(evt) {
			return false
		}
		if terminal, _ := terminalRunSignal(evt); terminal {
			return true
		}
	}
	return false
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
