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
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TrackAGUI is the AG-UI track identifier.
const TrackAGUI session.Track = "agui"

// Tracker is the interface for tracking AG-UI events.
type Tracker interface {
	AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error
	GetEvents(ctx context.Context, key session.Key) (*session.TrackEvents, error)
}

// tracker is the implementation of the Tracker interface.
type tracker struct {
	sessionService session.Service
	trackService   session.TrackService
}

// New creates a new tracker.
func New(service session.Service) (Tracker, error) {
	trackService, ok := service.(session.TrackService)
	if !ok {
		return nil, fmt.Errorf("session service does not implement track service")
	}
	return &tracker{
		sessionService: service,
		trackService:   trackService,
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
	payload, err := event.ToJSON()
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	timestamp := time.Now()
	if ts := event.Timestamp(); ts != nil {
		timestamp = time.UnixMilli(*ts)
	}
	trackEvent := &session.TrackEvent{
		Track:     TrackAGUI,
		Payload:   json.RawMessage(append([]byte(nil), payload...)),
		Timestamp: timestamp,
	}
	sess, err := t.sessionService.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		sess, err = t.sessionService.CreateSession(ctx, key, session.StateMap{})
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	if err := t.trackService.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
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
