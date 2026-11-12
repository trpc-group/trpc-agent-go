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
	if trackEvents == nil || r.flushInterval <= 0 || trackEndsWithTerminalRunEvent(trackEvents.Events) {
		return false
	}
	return len(trackEvents.Events) > 0 || r.isRunning(key)
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
	latest          snapshotRunID
	observed        map[snapshotRunID]snapshotRun
	hasBoundary     bool
	completed       bool
	ambiguousLegacy bool
}

type snapshotRun struct {
	hasBoundary bool
	finished    bool
}

type orderedSnapshotRun struct {
	owner    snapshotRunID
	sequence uint64
}

func trackEndsWithTerminalRunEvent(events []session.TrackEvent) bool {
	runs := snapshotRuns{observed: make(map[snapshotRunID]snapshotRun)}
	latestByEpoch := make(map[string]orderedSnapshotRun)
	var previous aguievents.EventType
	for _, event := range events {
		id, order := track.HistoryRun(event.Payload)
		latest := latestByEpoch[order.Epoch]
		ordered := id != "" && order.Epoch != "" && order.Sequence > 0
		if ordered && order.Sequence < latest.sequence {
			previous = ""
			continue
		}
		evt, err := aguievents.EventFromJSON(event.Payload)
		if err != nil {
			continue
		}
		terminal, _ := terminalRunSignal(evt)
		eventType := evt.Type()
		userMessage := isUserMessageEvent(evt)
		if id != "" {
			previous = ""
			boundary := eventType == aguievents.EventTypeRunStarted || userMessage
			var orderedBoundary bool
			if ordered && order.Sequence > latest.sequence {
				// An admission orders the owners even if one startup batch was
				// delayed or lost. Drop only older runs from the same epoch.
				delete(runs.observed, latest.owner)
				latestByEpoch[order.Epoch] = orderedSnapshotRun{
					owner:    snapshotRunID{value: id, tracked: true},
					sequence: order.Sequence,
				}
				// A response or terminal can establish a boundary only when
				// there is no boundary or this order supersedes its owner.
				orderedBoundary = !runs.hasBoundary || latest.sequence != 0 && runs.latest == latest.owner
			}
			runs.observeTracked(id, boundary, orderedBoundary, terminal)
			continue
		}

		switch {
		case eventType == aguievents.EventTypeRunStarted:
			runs.observeLegacyBoundary(evt.RunID())
		case userMessage:
			// Legacy tracks can place a text user message just after its start.
			if previous != aguievents.EventTypeRunStarted || eventType == aguievents.EventTypeCustom {
				runs.observeLegacyBoundary("")
			}
		case terminal:
			if runs.matchesLegacyTerminal(evt.RunID()) {
				runs.completed = true
			}
		case runs.completed && isSnapshotResponseEvent(eventType):
			// An untagged response can belong to a newer run whose startup
			// records are missing, even when an older run is still active.
			runs.ambiguousLegacy = runs.hasOtherActiveRun()
			runs.latest, runs.hasBoundary, runs.completed = snapshotRunID{}, true, false
		}
		previous = eventType
	}
	return runs.completed && !runs.hasUnresolvedRun(len(latestByEpoch) > 1)
}

func (r *snapshotRuns) observeTracked(id string, boundary, orderedBoundary, terminal bool) {
	owner := snapshotRunID{value: id, tracked: true}
	run := r.observed[owner]
	// The first visible boundary establishes order. Later events from
	// the same invocation only update that invocation's state.
	if (boundary || orderedBoundary) && !run.hasBoundary {
		run.hasBoundary = boundary
		r.latest, r.hasBoundary, r.completed = owner, true, run.finished
		r.ambiguousLegacy = false
	}
	if terminal {
		run.finished = true
		if owner == r.latest || !r.hasBoundary {
			r.completed = true
		}
	}
	r.observed[owner] = run
}

func (r *snapshotRuns) observeLegacyBoundary(id string) {
	owner := snapshotRunID{value: id}
	if id != "" {
		r.observed[owner] = snapshotRun{hasBoundary: true}
	}
	r.ambiguousLegacy = id == "" && r.hasBoundary && r.latest.value == "" && !r.completed
	r.latest, r.hasBoundary, r.completed = owner, true, false
}

func (r *snapshotRuns) matchesLegacyTerminal(id string) bool {
	if id != "" {
		owner := snapshotRunID{value: id}
		run, observed := r.observed[owner]
		run.finished = true
		r.observed[owner] = run
		if r.latest.value != "" {
			return owner == r.latest
		}
		return !observed && !r.ambiguousLegacy
	}
	// Older callbacks may omit the terminal's run ID. Infer ownership
	// only among untagged runs with no other active candidate.
	if r.latest.tracked || r.ambiguousLegacy || r.hasOtherActiveRun() {
		return false
	}
	if r.latest.value != "" {
		run := r.observed[r.latest]
		run.finished = true
		r.observed[r.latest] = run
	}
	return true
}

func (r *snapshotRuns) hasOtherActiveRun() bool {
	for id, run := range r.observed {
		if !run.finished && id != r.latest {
			return true
		}
	}
	return false
}

func (r *snapshotRuns) hasUnresolvedRun(multipleEpochs bool) bool {
	for id, run := range r.observed {
		if !run.finished && (!run.hasBoundary || multipleEpochs && id.tracked && id != r.latest) {
			return true
		}
	}
	return false
}

func isSnapshotResponseEvent(eventType aguievents.EventType) bool {
	switch eventType {
	case aguievents.EventTypeTextMessageStart, aguievents.EventTypeTextMessageContent, aguievents.EventTypeTextMessageChunk,
		aguievents.EventTypeReasoningMessageStart, aguievents.EventTypeReasoningMessageContent, aguievents.EventTypeReasoningMessageChunk,
		aguievents.EventTypeToolCallStart, aguievents.EventTypeToolCallArgs, aguievents.EventTypeToolCallChunk,
		aguievents.EventTypeToolCallResult:
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
