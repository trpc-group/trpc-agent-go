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
	// AppendEvent appends an AG-UI event to the session track.
	AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error
	// GetEvents retrieves the AG-UI track events from the session.
	GetEvents(ctx context.Context, key session.Key) (*session.TrackEvents, error)
	// Flush flushes any pending aggregated events for the given session key.
	Flush(ctx context.Context, key session.Key) error
}

// tracker is the implementation of the Tracker interface.
type tracker struct {
	sessionService    session.Service                       // sessionService handles session lifecycle.
	trackService      session.TrackService                  // trackService persists track events.
	mu                sync.Mutex                            // mu guards aggregators.
	aggregatorFactory aggregator.Factory                    // aggregatorFactory builds aggregators for new sessions.
	aggregators       map[session.Key]aggregator.Aggregator // aggregators caches per-session aggregators.
	aggregationOption []aggregator.Option                   // aggregationOption applies to newly built aggregators.
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
		aggregators:       make(map[session.Key]aggregator.Aggregator),
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
	agg := t.getAggregator(key)
	aggregated, err := agg.Append(ctx, event)
	if err != nil {
		return fmt.Errorf("aggregate event: %w", err)
	}
	return t.persistEvents(ctx, key, aggregated)
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
	agg := t.getAggregator(key)
	aggregated, err := agg.Flush(ctx)
	if err != nil {
		return fmt.Errorf("flush aggregated events: %w", err)
	}
	t.deleteAggregator(key)
	return t.persistEvents(ctx, key, aggregated)
}

// persistEvents ensures the session exists and appends track events to storage.
func (t *tracker) persistEvents(ctx context.Context, key session.Key, events []aguievents.Event) error {
	if len(events) == 0 {
		return nil
	}
	sess, err := t.ensureSessionExists(ctx, key)
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
		if err := t.trackService.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
			multierr.AppendInto(&overallErr, fmt.Errorf("append track event %v: %w", trackEvent, err))
		}
	}
	if overallErr != nil {
		return fmt.Errorf("persist events: %v", err)
	}
	return nil
}

// ensureSessionExists fetches the session or creates one when absent.
func (t *tracker) ensureSessionExists(ctx context.Context, key session.Key) (*session.Session, error) {
	sess, err := t.sessionService.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if sess != nil {
		return sess, nil
	}
	sess, err = t.sessionService.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// getAggregator returns the cached aggregator for the session, creating one when missing.
func (t *tracker) getAggregator(key session.Key) aggregator.Aggregator {
	t.mu.Lock()
	defer t.mu.Unlock()
	agg, ok := t.aggregators[key]
	if !ok || agg == nil {
		agg = t.aggregatorFactory(t.aggregationOption...)
		t.aggregators[key] = agg
	}
	return agg
}

// deleteAggregator removes the cached aggregator for the session key.
func (t *tracker) deleteAggregator(key session.Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.aggregators, key)
}
