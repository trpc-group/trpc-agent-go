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
		threadID:      runAgentInput.ThreadID,
		runID:         runAgentInput.RunID,
		userID:        userID,
		enableTrack:   false,
		runAgentInput: runAgentInput,
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

	var pageReq *MessagesSnapshotPageRequest
	if _, ok := r.sessionService.(session.TrackEventPageService); ok {
		var err error
		pageReq, err = r.resolveMessagesSnapshotSessionPage(ctx, input)
		if err != nil {
			log.ErrorfContext(ctx, "agui messages snapshot: threadID: %s, runID: %s, resolve page: %v",
				threadID, runID, err)
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("resolve page: %v", err),
				aguievents.WithRunID(runID)), input)
			return
		}
	}
	input.messagesSnapshotPage = pageReq
	messagesSnapshotEvent, trackEvents, err := r.getMessagesSnapshotEvent(ctx, input.key, input.messagesSnapshotPage)
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

	if input.messagesSnapshotPage == nil && r.messagesSnapshotFollowEnabled && r.shouldFollowMessagesSnapshot(input.key, trackEvents) {
		r.messagesSnapshotFollow(ctx, input, events, trackEvents)
		return
	}
	// Emit a RUN_FINISHED event to signal downstream consumers there is no more data.
	if !r.emitEvent(ctx, events, aguievents.NewRunFinishedEvent(threadID, runID), input) {
		return
	}
}

func (r *runner) resolveMessagesSnapshotSessionPage(
	ctx context.Context,
	input *runInput,
) (*MessagesSnapshotPageRequest, error) {
	if r.messagesSnapshotSessionPageResolver == nil {
		return nil, nil
	}
	req, err := r.messagesSnapshotSessionPageResolver(ctx, input.runAgentInput, input.key)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, nil
	}
	if req.EventLimit <= 0 {
		return nil, errors.New("messages snapshot session page requires eventLimit > 0")
	}
	copied := *req
	return &copied, nil
}

// getMessagesSnapshotEvent loads AG-UI track events and converts them to an AG-UI MessagesSnapshotEvent.
// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
func (r *runner) getMessagesSnapshotEvent(
	ctx context.Context,
	sessionKey session.Key,
	pageReq *MessagesSnapshotPageRequest,
) (*aguievents.MessagesSnapshotEvent, *session.TrackEvents, error) {
	if pageReq != nil {
		if pager, ok := r.sessionService.(session.TrackEventPageService); ok {
			return r.getMessagesSnapshotPageEvent(ctx, sessionKey, pageReq, pager)
		}
	}
	return r.getMessagesSnapshotFullEvent(ctx, sessionKey)
}

func (r *runner) getMessagesSnapshotFullEvent(
	ctx context.Context,
	sessionKey session.Key,
) (*aguievents.MessagesSnapshotEvent, *session.TrackEvents, error) {
	trackEvents, err := r.tracker.GetEvents(ctx, sessionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("get track events: %w", err)
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
	)
	if err != nil {
		err = fmt.Errorf("reduce track events: %w", err)
	}
	log.DebugfContext(ctx, "agui messages snapshot: app=%s, user=%s, session=%s, trackEvents=%d, eventsForReduce=%d, safeForFollow=%t, messages=%d, reduceErr=%v",
		sessionKey.AppName, sessionKey.UserID, sessionKey.SessionID, len(trackEvents.Events), len(eventsForReduce), safeForFollow, len(messages), err)
	event := aguievents.NewMessagesSnapshotEvent(messages)
	r.attachMessagesSnapshotRawEvent(event, eventsForReduce, nil)
	if !safeForFollow {
		trackEvents = nil
	}
	return event, trackEvents, err
}

func (r *runner) getMessagesSnapshotPageEvent(
	ctx context.Context,
	sessionKey session.Key,
	pageReq *MessagesSnapshotPageRequest,
	pager session.TrackEventPageService,
) (*aguievents.MessagesSnapshotEvent, *session.TrackEvents, error) {
	page, err := pager.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        sessionKey,
		Track:      track.TrackAGUI,
		Cursor:     pageReq.Cursor,
		EventLimit: pageReq.EventLimit,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("get track event page: %w", err)
	}
	eventsForReduce, pageMetadata := trimTrackEventPageEntriesToHistoryStart(
		page.Entries,
		pageReq.Cursor,
		page.HasMore,
	)
	messages, err := reduce.Reduce(
		sessionKey.AppName,
		sessionKey.UserID,
		eventsForReduce,
		reduce.WithRunLifecycleEvents(r.messagesSnapshotRunLifecycleEventsEnabled),
	)
	if err != nil {
		err = fmt.Errorf("reduce track events: %w", err)
	}
	log.DebugfContext(ctx, "agui messages snapshot page: app=%s, user=%s, session=%s, pageEntries=%d, eventsForReduce=%d, hasMore=%t, messages=%d, reduceErr=%v",
		sessionKey.AppName, sessionKey.UserID, sessionKey.SessionID, len(page.Entries), len(eventsForReduce), pageMetadata.HasMore, len(messages), err)
	event := aguievents.NewMessagesSnapshotEvent(messages)
	r.attachMessagesSnapshotRawEvent(event, eventsForReduce, &pageMetadata)
	return event, nil, err
}

func (r *runner) attachMessagesSnapshotRawEvent(
	event *aguievents.MessagesSnapshotEvent,
	events []session.TrackEvent,
	page *source.SnapshotPageMetadata,
) {
	metadata := source.SnapshotMetadata{}
	if r.eventSourceMetadataEnabled {
		metadata = source.BuildSnapshotMetadata(
			events,
			source.WithRunLifecycleEvents(r.messagesSnapshotRunLifecycleEventsEnabled),
		)
	}
	if page != nil {
		pageCopy := *page
		metadata.Page = &pageCopy
	}
	if !metadata.IsZero() {
		event.GetBaseEvent().RawEvent = metadata
	}
}

func trimTrackEventPageEntriesToHistoryStart(
	entries []session.TrackEventPageEntry,
	requestCursor string,
	sessionHasMore bool,
) ([]session.TrackEvent, source.SnapshotPageMetadata) {
	page := source.SnapshotPageMetadata{
		Cursor:  requestCursor,
		HasMore: sessionHasMore,
	}
	if len(entries) == 0 {
		return nil, page
	}
	for i, entry := range entries {
		if isUserMessageTrackEvent(entry.Event) {
			page.Cursor = entry.Cursor
			page.HasMore = sessionHasMore || i > 0
			events := make([]session.TrackEvent, 0, len(entries)-i)
			for _, kept := range entries[i:] {
				events = append(events, kept.Event)
			}
			return events, page
		}
	}
	page.HasMore = true
	return nil, page
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

func isEmptyTrackEventsError(err error) bool {
	return errors.Is(err, session.ErrTracksEmpty) || errors.Is(err, session.ErrTrackEventsNotFound)
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
