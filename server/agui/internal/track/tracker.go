//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package track implements the tracker for AG-UI events in the session.
package track

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"go.uber.org/multierr"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TrackAGUI is the AG-UI track identifier.
const TrackAGUI session.Track = "agui"

// Tracker is the interface for tracking AG-UI events.
type Tracker interface {
	// AppendEvent queues an AG-UI event for the next persistence flush.
	AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error
	// GetEvents retrieves the AG-UI track events from the session.
	GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error)
	// Flush flushes buffered events for the given session key without closing the session state.
	Flush(ctx context.Context, key session.Key) error
	// Close flushes buffered events and releases the session state for the given session key.
	Close(ctx context.Context, key session.Key) error
}

type trackEventReader interface {
	GetTrackEvents(ctx context.Context, key session.Key, track session.Track, opts ...session.Option) (*session.TrackEvents, error)
}

type requestIDContextKey struct{}

type pendingTrackEvent struct {
	payload   json.RawMessage
	requestID string
}

// ContextWithRequestID associates tracked AG-UI events with the effective
// core Runner request without changing their protocol RunID.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func requestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// tracker is the implementation of the Tracker interface.
type tracker struct {
	sessionService    session.Service               // sessionService handles session lifecycle.
	trackService      session.TrackService          // trackService persists track events.
	mu                sync.Mutex                    // mu guards the sessionStates map.
	aggregatorFactory aggregator.Factory            // aggregatorFactory builds aggregators for new sessions.
	aggregationOption []aggregator.Option           // aggregationOption applies to newly built aggregators.
	sessionStates     map[session.Key]*sessionState // sessionStates stores the state of each session.
}

// sessionState stores the state of a session.
type sessionState struct {
	mu         sync.Mutex            // mu guards the aggregator and pending events.
	persistMu  sync.Mutex            // persistMu serializes storage writes for the session.
	aggregator aggregator.Aggregator // aggregator aggregates events.
	pending    []pendingTrackEvent   // pending stores immutable events waiting for persistence.
	session    *session.Session      // session caches the ensured session to avoid repeated lookups.
	closing    bool                  // closing rejects appends while Close is draining the final batch.
}

// New creates a new tracker.
func New(service session.Service, opt ...Option) (Tracker, error) {
	trackService, ok := service.(session.TrackService)
	if !ok {
		return nil, fmt.Errorf("session service does not implement track service")
	}
	opts := newOptions(opt...)
	return &tracker{
		sessionService:    service,
		trackService:      trackService,
		aggregatorFactory: opts.aggregatorFactory,
		aggregationOption: opts.aggregationOption,
		sessionStates:     make(map[session.Key]*sessionState),
	}, nil
}

// AppendEvent queues an AG-UI event for the next persistence flush.
func (t *tracker) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	if event == nil {
		return fmt.Errorf("event is nil")
	}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("session key: %w", err)
	}
	state := t.getSessionState(ctx, key)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closing {
		return fmt.Errorf("session state is closing: %v", key)
	}
	aggregated, err := state.aggregator.Append(ctx, event)
	if err != nil {
		return fmt.Errorf("aggregate event: %w", err)
	}
	payloads, err := snapshotEvents(
		aggregated,
		requestIDFromContext(ctx),
	)
	state.pending = append(state.pending, payloads...)
	if err != nil {
		return fmt.Errorf("snapshot aggregated events: %w", err)
	}
	return nil
}

// GetEvents retrieves the AG-UI track events from the session.
func (t *tracker) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	if reader, ok := t.sessionService.(trackEventReader); ok {
		trackEvents, err := reader.GetTrackEvents(ctx, key, TrackAGUI, opts...)
		if err != nil {
			return nil, fmt.Errorf("get track events: %w", err)
		}
		return trackEvents, nil
	}
	sess, err := t.sessionService.GetSession(ctx, key, opts...)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found")
	}
	trackEvents, err := sess.GetTrackEvents(TrackAGUI)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	return trackEvents, nil
}

// Flush flushes buffered events for the given session key without closing the session state.
func (t *tracker) Flush(ctx context.Context, key session.Key) error {
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("session key: %w", err)
	}
	state := t.getExistingSessionState(key)
	if state == nil {
		return fmt.Errorf("session state not found: %v", key)
	}
	if err := t.flush(ctx, key, state); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// Close flushes buffered events and releases the session state for the given session key.
func (t *tracker) Close(ctx context.Context, key session.Key) error {
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("session key: %w", err)
	}
	state := t.getExistingSessionState(key)
	if state == nil {
		return nil
	}
	err := t.closeState(ctx, key, state)
	t.deleteSessionState(key, state)
	if err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// persistEvents ensures the session exists and appends track events to storage.
func (t *tracker) persistEvents(
	ctx context.Context,
	key session.Key,
	state *sessionState,
	events []pendingTrackEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	sess, err := t.ensureSessionExists(ctx, key, state)
	if err != nil {
		return fmt.Errorf("ensure session exists: %w", err)
	}
	var overallErr error
	for _, event := range events {
		trackEvent := &session.TrackEvent{
			Track:     TrackAGUI,
			RequestID: event.requestID,
			Payload:   json.RawMessage(append([]byte(nil), event.payload...)),
			Timestamp: time.Now(),
		}
		if sess == nil {
			multierr.AppendInto(&overallErr, fmt.Errorf("append track event %v: session unavailable", trackEvent))
			break
		}
		if err := t.trackService.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
			state.session = nil
			multierr.AppendInto(&overallErr, fmt.Errorf("append track event %v: %w", trackEvent, err))
			break
		}
	}
	if overallErr != nil {
		return fmt.Errorf("persist events: %w", overallErr)
	}
	return nil
}

// snapshotEvents serializes aggregator outputs before their ownership is returned to the caller.
func snapshotEvents(
	events []aguievents.Event,
	requestID string,
) ([]pendingTrackEvent, error) {
	payloads := make([]pendingTrackEvent, 0, len(events))
	var overallErr error
	for _, event := range events {
		payload, err := event.ToJSON()
		if err != nil {
			multierr.AppendInto(&overallErr, fmt.Errorf("marshal event %v: %w", event, err))
			continue
		}
		eventRequestID := requestID
		if eventRequestID == "" {
			eventRequestID = event.RunID()
		}
		payloads = append(payloads, pendingTrackEvent{
			payload:   json.RawMessage(append([]byte(nil), payload...)),
			requestID: eventRequestID,
		})
	}
	return payloads, overallErr
}

// ensureSessionExists fetches the session or creates one when absent.
func (t *tracker) ensureSessionExists(ctx context.Context, key session.Key, state *sessionState) (*session.Session, error) {
	if state.session != nil {
		return state.session, nil
	}
	sess, err := t.sessionService.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if sess != nil {
		state.session = sess
		return state.session, nil
	}
	sess, err = t.sessionService.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	state.session = sess
	return state.session, nil
}

// getSessionState returns the cached session state for the key, creating one when missing.
func (t *tracker) getSessionState(ctx context.Context, key session.Key) *sessionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if state, ok := t.sessionStates[key]; ok {
		return state
	}
	state := &sessionState{
		aggregator: t.aggregatorFactory(ctx, t.aggregationOption...),
	}
	t.sessionStates[key] = state
	return state
}

// getExistingSessionState returns the cached session state for the key when it exists.
func (t *tracker) getExistingSessionState(key session.Key) *sessionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionStates[key]
}

// deleteSessionState removes the cached session state when it still matches state.
func (t *tracker) deleteSessionState(key session.Key, state *sessionState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionStates[key] == state {
		delete(t.sessionStates, key)
	}
}

// flush flushes the session state.
func (t *tracker) flush(ctx context.Context, key session.Key, state *sessionState) error {
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	batch, drainErr := t.drainPending(ctx, state)
	persistErr := t.persistEvents(ctx, key, state, batch)
	return multierr.Combine(drainErr, persistErr)
}

func (t *tracker) closeState(ctx context.Context, key session.Key, state *sessionState) error {
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	batch, drainErr := t.drainPendingForClose(ctx, state)
	persistErr := t.persistEvents(ctx, key, state, batch)
	return multierr.Combine(drainErr, persistErr)
}

func (t *tracker) drainPending(ctx context.Context, state *sessionState) ([]pendingTrackEvent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return t.drainPendingLocked(ctx, state)
}

func (t *tracker) drainPendingForClose(ctx context.Context, state *sessionState) ([]pendingTrackEvent, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closing = true
	return t.drainPendingLocked(ctx, state)
}

func (t *tracker) drainPendingLocked(ctx context.Context, state *sessionState) ([]pendingTrackEvent, error) {
	events, err := state.aggregator.Flush(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggregator flush: %w", err)
	}
	payloads, snapshotErr := snapshotEvents(
		events,
		requestIDFromContext(ctx),
	)
	batch := make([]pendingTrackEvent, 0, len(state.pending)+len(payloads))
	batch = append(batch, state.pending...)
	batch = append(batch, payloads...)
	state.pending = nil
	if snapshotErr != nil {
		return batch, fmt.Errorf("snapshot aggregated events: %w", snapshotErr)
	}
	return batch, nil
}
