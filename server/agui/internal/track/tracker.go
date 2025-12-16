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
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TrackAGUI is the AG-UI track identifier.
const TrackAGUI session.Track = "agui"

// Tracker is the interface for tracking AG-UI events.
type Tracker interface {
	// AppendEvent appends an AG-UI event to the session track.
	AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error
	// GetEvents retrieves the AG-UI track events from the session.
	GetEvents(ctx context.Context, key session.Key) (*session.TrackEvents, error)
	// Flush flushes any pending aggregated events for the given session key.
	Flush(ctx context.Context, key session.Key) error
}

// tracker is the implementation of the Tracker interface.
type tracker struct {
	sessionService    session.Service               // sessionService handles session lifecycle.
	trackService      session.TrackService          // trackService persists track events.
	mu                sync.Mutex                    // mu guards the sessionStates map.
	aggregatorFactory aggregator.Factory            // aggregatorFactory builds aggregators for new sessions.
	aggregationOption []aggregator.Option           // aggregationOption applies to newly built aggregators.
	sessionStates     map[session.Key]*sessionState // sessionStates stores the state of each session.
	flushInterval     time.Duration                 // flushInterval is the interval for flushing the session state.
}

// sessionState stores the state of a session.
type sessionState struct {
	mu         sync.Mutex            // mu guards the aggregator and the done channel.
	aggregator aggregator.Aggregator // aggregator aggregates events.
	done       chan struct{}         // done is closed when the session state is removed.
	session    *session.Session      // session caches the ensured session to avoid repeated lookups.
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
		flushInterval:     opts.flushInterval,
	}, nil
}

// AppendEvent appends an AG-UI event to the session track.
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
	aggregated, err := state.aggregator.Append(ctx, event)
	if err != nil {
		return fmt.Errorf("aggregate event: %w", err)
	}
	return t.persistEvents(ctx, key, state, aggregated)
}

// GetEvents retrieves the AG-UI track events from the session.
func (t *tracker) GetEvents(ctx context.Context, key session.Key) (*session.TrackEvents, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	sess, err := t.sessionService.GetSession(ctx, key)
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

// Flush flushes any pending aggregated events for the given session key.
func (t *tracker) Flush(ctx context.Context, key session.Key) error {
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("session key: %w", err)
	}
	defer t.deleteSessionState(key)
	state := t.getSessionState(ctx, key)
	if err := t.flush(ctx, key, state); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}

// persistEvents ensures the session exists and appends track events to storage.
func (t *tracker) persistEvents(ctx context.Context, key session.Key, state *sessionState, events []aguievents.Event) error {
	if len(events) == 0 {
		return nil
	}
	sess, err := t.ensureSessionExists(ctx, key, state)
	if err != nil {
		return fmt.Errorf("ensure session exists: %w", err)
	}
	var overallErr error
	for _, e := range events {
		payload, err := e.ToJSON()
		if err != nil {
			multierr.AppendInto(&overallErr, fmt.Errorf("marshal event %v: %w", e, err))
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
	}
	if overallErr != nil {
		return fmt.Errorf("persist events: %v", overallErr)
	}
	return nil
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
		done:       make(chan struct{}),
	}
	t.sessionStates[key] = state
	if t.flushInterval > 0 {
		state.done = make(chan struct{})
		flushCtx := agent.CloneContext(ctx)
		go t.flushPeriodically(flushCtx, key, state)
	}
	return state
}

// deleteSessionState removes the cached session state for the session key.
func (t *tracker) deleteSessionState(key session.Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.flushInterval > 0 {
		close(t.sessionStates[key].done)
	}
	delete(t.sessionStates, key)
}

// flushPeriodically flushes the session state periodically.
func (t *tracker) flushPeriodically(ctx context.Context, key session.Key, state *sessionState) {
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := t.flush(ctx, key, state); err != nil {
				log.WarnfContext(
					ctx,
					"flush: %v",
					err,
				)
			}
		case <-state.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// flush flushes the session state.
func (t *tracker) flush(ctx context.Context, key session.Key, state *sessionState) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	events, err := state.aggregator.Flush(ctx)
	if err != nil {
		return fmt.Errorf("aggregator flush: %w", err)
	}
	return t.persistEvents(ctx, key, state, events)
}
