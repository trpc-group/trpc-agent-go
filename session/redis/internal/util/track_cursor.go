//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package util

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TrackCursor identifies a persisted position within one storage generation.
type TrackCursor struct {
	Scope      string
	Generation string
	Epoch      string
	Position   string
}

// DecodeTrackCursor validates a token's storage, session and track scope.
func DecodeTrackCursor(token, backend string, key session.Key, track session.Track) (TrackCursor, error) {
	scopeBytes, _ := json.Marshal([]string{backend, key.AppName, key.UserID, key.SessionID, string(track)})
	scope := fmt.Sprintf("%x", sha256.Sum256(scopeBytes))
	c := TrackCursor{Scope: scope, Position: "0"}
	if token == "" {
		return c, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return c, fmt.Errorf("invalid track cursor")
	}
	c = TrackCursor{}
	if err := json.Unmarshal(raw, &c); err != nil || c.Scope != scope || c.Generation == "" || c.Position == "" {
		return c, fmt.Errorf("invalid track cursor scope or generation")
	}
	return c, nil
}

// TrackCursorResult decodes an atomic read's metadata and event payloads.
func TrackCursorResult(raw []string, c TrackCursor, track session.Track) (*session.TrackEvents, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing track cursor metadata")
	}
	if err := json.Unmarshal([]byte(raw[0]), &c); err != nil {
		return nil, err
	}
	token, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	result := &session.TrackEvents{Track: track, Cursor: base64.RawURLEncoding.EncodeToString(token)}
	for _, payload := range raw[1:] {
		var event session.TrackEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, fmt.Errorf("decode track event: %w", err)
		}
		result.Events = append(result.Events, event)
	}
	return result, nil
}
