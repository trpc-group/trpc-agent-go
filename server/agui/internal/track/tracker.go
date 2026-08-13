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
	pending    []aguievents.Event    // pending stores events waiting for the next persistence flush.
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
	state.pending = append(state.pending, aggregated...)
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
	events []aguievents.Event,
) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	sess, err := t.ensureSessionExists(ctx, key, state)
	if err != nil {
		return 0, fmt.Errorf("ensure session exists: %w", err)
	}
	var overallErr error
	processed := 0
	for _, e := range events {
		payload, err := e.ToJSON()
		if err != nil {
			multierr.AppendInto(&overallErr, fmt.Errorf("marshal event %v: %w", e, err))
			processed++
			continue
		}
		trackEvent := &session.TrackEvent{
			Track:     TrackAGUI,
			Payload:   json.RawMessage(append([]byte(nil), payload...)),
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
		processed++
	}
	if overallErr != nil {
		return processed, fmt.Errorf("persist events: %w", overallErr)
	}
	return processed, nil
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
	batch, err := t.drainPending(ctx, state)
	if err != nil {
		return err
	}
	processed, persistErr := t.persistEvents(ctx, key, state, batch)
	if persistErr != nil {
		state.mu.Lock()
		t.prependPending(state, batch[processed:])
		state.mu.Unlock()
		return persistErr
	}
	return nil
}

func (t *tracker) closeState(ctx context.Context, key session.Key, state *sessionState) error {
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	batch, err := t.drainPendingForClose(ctx, state)
	if err != nil {
		return err
	}
	_, persistErr := t.persistEvents(ctx, key, state, batch)
	return persistErr
}

func (t *tracker) drainPending(ctx context.Context, state *sessionState) ([]aguievents.Event, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return t.drainPendingLocked(ctx, state)
}

func (t *tracker) drainPendingForClose(ctx context.Context, state *sessionState) ([]aguievents.Event, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closing = true
	return t.drainPendingLocked(ctx, state)
}

func (t *tracker) drainPendingLocked(ctx context.Context, state *sessionState) ([]aguievents.Event, error) {
	events, err := state.aggregator.Flush(ctx)
	if err != nil {
		return nil, fmt.Errorf("aggregator flush: %w", err)
	}
	batch := make([]aguievents.Event, 0, len(state.pending)+len(events))
	batch = append(batch, state.pending...)
	batch = append(batch, events...)
	state.pending = nil
	return batch, nil
}

func (t *tracker) prependPending(state *sessionState, events []aguievents.Event) {
	if len(events) == 0 {
		return
	}
	pending := make([]aguievents.Event, 0, len(events)+len(state.pending))
	pending = append(pending, events...)
	pending = append(pending, state.pending...)
	state.pending = pending
}
