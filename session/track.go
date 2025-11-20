//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const (
	// tracksStateKey stores the track index on the session state.
	tracksStateKey = "tracks"
)

// TrackService provides the interface for appending track events to a session.
type TrackService interface {
	AppendTrackEvent(ctx context.Context, sess *Session, event *TrackEvent, opts ...Option) error
}

// Track represents a logical track that stores track events.
type Track string

// TrackEvent represents a track event.
type TrackEvent struct {
	Track     Track           `json:"track"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

// TrackEvents bundles the track events.
type TrackEvents struct {
	Track  Track        `json:"track"`
	Events []TrackEvent `json:"events"`
}

// TracksFromState returns the tracks stored in the session state.
func TracksFromState(state StateMap) ([]Track, error) {
	if state == nil {
		return nil, nil
	}
	raw, ok := state[tracksStateKey]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var tracks []Track
	if err := json.Unmarshal(raw, &tracks); err != nil {
		return nil, fmt.Errorf("decode track index: %w", err)
	}
	return tracks, nil
}

// ensureTrackExists ensures the track exists in the session state.
func ensureTrackExists(state StateMap, track Track) error {
	if state == nil {
		return fmt.Errorf("state is nil")
	}
	tracks, err := TracksFromState(state)
	if err != nil {
		return fmt.Errorf("get tracks from state: %w", err)
	}
	if slices.Contains(tracks, track) {
		return nil
	}
	tracks = append(tracks, track)
	encoded, err := json.Marshal(tracks)
	if err != nil {
		return fmt.Errorf("encode track index: %w", err)
	}
	state[tracksStateKey] = encoded
	return nil
}
