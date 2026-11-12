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
	"github.com/google/uuid"
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

type snapshotRunID struct {
	value   string
	tracked bool // Internal identities and legacy wire IDs are separate namespaces.
}

type snapshotRuns struct {
	latest        snapshotRunID
	active        map[snapshotRunID]bool // Retain completed IDs to recognize late events.
	startedMillis int64
	completed     bool
}

func trackEndsWithTerminalRunEvent(events []session.TrackEvent) bool {
	runs := snapshotRuns{active: make(map[snapshotRunID]bool)}
	var previous aguievents.EventType
	for _, event := range events {
		evt, err := aguievents.EventFromJSON(event.Payload)
		if err != nil {
			continue
		}
		terminal, _ := terminalRunSignal(evt)
		if id := track.HistoryRunID(event.Payload); id != "" {
			runs.observeTracked(id, terminal)
			previous = ""
			continue
		}

		eventType := evt.Type()
		switch {
		case eventType == aguievents.EventTypeRunStarted:
			runs.observeLegacyBoundary(evt.RunID(), evt.Timestamp(), event.Timestamp)
		case isUserMessageEvent(evt):
			// Legacy tracks can place a text user message just after its start.
			if previous != aguievents.EventTypeRunStarted || eventType == aguievents.EventTypeCustom {
				runs.observeLegacyBoundary("", evt.Timestamp(), event.Timestamp)
			}
		case terminal:
			if runs.matchesLegacyTerminal(evt.RunID()) {
				runs.completed = true
			}
		case runs.completed && isSnapshotResponseEvent(eventType) && !runs.hasOtherActiveRun():
			// With no observed run that could own this response, a legacy
			// startup write may have failed before a new run produced output.
			runs.latest, runs.completed = snapshotRunID{}, false
		}
		previous = eventType
	}
	return runs.completed
}

func (r *snapshotRuns) observeTracked(id string, terminal bool) {
	owner := snapshotRunID{value: id, tracked: true}
	if _, observed := r.active[owner]; !observed {
		r.active[owner] = true
		// The identity is assigned when the run starts. Its first persisted
		// event may arrive after a newer run if a startup write failed.
		started := trackedRunStartMillis(id)
		if r.latest.tracked && r.latest.value < id ||
			!r.latest.tracked && (started == 0 || started >= r.startedMillis) {
			r.latest, r.startedMillis, r.completed = owner, started, false
		}
	}
	if terminal {
		r.active[owner] = false
		if owner == r.latest {
			r.completed = true
		}
	}
}

func trackedRunStartMillis(id string) int64 {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 {
		return 0
	}
	sec, nsec := parsed.Time().UnixTime()
	return time.Unix(sec, nsec).UnixMilli()
}

func (r *snapshotRuns) observeLegacyBoundary(id string, emittedMillis *int64, persistedAt time.Time) {
	owner := snapshotRunID{value: id}
	if id != "" {
		r.active[owner] = true
	}
	var started int64
	if emittedMillis != nil {
		started = *emittedMillis
	} else if !persistedAt.IsZero() {
		started = persistedAt.UnixMilli()
	}
	// During a rolling upgrade, a legacy start can be persisted after a
	// tracked run even though the legacy runner emitted its start earlier.
	if r.latest.tracked && started != 0 && started < r.startedMillis {
		return
	}
	r.latest, r.startedMillis, r.completed = owner, started, false
}

func (r *snapshotRuns) matchesLegacyTerminal(id string) bool {
	if id != "" {
		owner := snapshotRunID{value: id}
		_, observed := r.active[owner]
		r.active[owner] = false
		if r.latest.value != "" {
			return owner == r.latest
		}
		return !observed
	}
	// Older callbacks may omit the terminal's run ID. Infer ownership
	// only among untagged runs with no other active candidate.
	if r.latest.tracked || r.hasOtherActiveRun() {
		return false
	}
	if r.latest.value != "" {
		r.active[r.latest] = false
	}
	return true
}

func (r *snapshotRuns) hasOtherActiveRun() bool {
	for id, active := range r.active {
		if active && id != r.latest {
			return true
		}
	}
	return false
}

func isSnapshotResponseEvent(eventType aguievents.EventType) bool {
	switch eventType {
	case aguievents.EventTypeTextMessageStart, aguievents.EventTypeTextMessageContent, aguievents.EventTypeTextMessageChunk,
		aguievents.EventTypeReasoningMessageStart, aguievents.EventTypeReasoningMessageContent, aguievents.EventTypeReasoningMessageChunk,
		aguievents.EventTypeToolCallStart, aguievents.EventTypeToolCallArgs, aguievents.EventTypeToolCallChunk:
		return true
	default:
		return false
	}
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
