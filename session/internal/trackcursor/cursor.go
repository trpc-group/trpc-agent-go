//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package trackcursor implements internal track continuation bindings.
package trackcursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Cursor binds an internal append position to one storage history.
type Cursor struct {
	Version    int    `json:"v"`
	Binding    string `json:"b"`
	Generation string `json:"g"`
	Position   uint64 `json:"p"`
}

// Decode validates a token's format and read scope; it is not authorization.
func Decode(raw, backend string, key session.Key, track session.Track) (Cursor, error) {
	scope, _ := json.Marshal([]string{backend, key.AppName, key.UserID, key.SessionID, string(track)})
	binding := fmt.Sprintf("%x", sha256.Sum256(scope))
	c := Cursor{Version: 1, Binding: binding}
	if raw == "" {
		return c, nil
	}
	if len(raw) > 8192 {
		return Cursor{}, session.ErrTrackCursorInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	c = Cursor{}
	if err != nil || json.Unmarshal(data, &c) != nil || c.Version != 1 ||
		c.Binding != binding ||
		(c.Generation == "" && c.Position != 0) || c.Position > math.MaxInt64 {
		return Cursor{}, session.ErrTrackCursorInvalid
	}
	return c, nil
}

// Encode serializes a cursor without exposing it in stored events or SSE data.
func Encode(c Cursor) string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

// Read runs the existing policy chain around one authoritative storage read.
func Read(ctx context.Context, key session.Key, track session.Track, cursor string,
	hooks []session.GetSessionHook,
	load func(*session.GetSessionContext) (*session.Session, string, error),
) (*session.TrackEvents, string, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	requestedCursor := cursor
	hctx := &session.GetSessionContext{Context: ctx, Key: key, Options: &session.Options{TrackCursor: &requestedCursor}}
	var nextCursor string
	reads := 0
	final := func(c *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
		reads++
		if reads != 1 || c.Options == nil || c.Context == nil {
			return nil, fmt.Errorf("%w: read policy must load storage once", session.ErrTrackCursorInvalid)
		}
		if c.Options.TrackCursor == nil || *c.Options.TrackCursor != cursor {
			return nil, fmt.Errorf("%w: read policy changed the continuation", session.ErrTrackCursorInvalid)
		}
		if err := c.Context.Err(); err != nil {
			return nil, err
		}
		if err := c.Key.CheckSessionKey(); err != nil {
			return nil, err
		}
		sess, next, err := load(c)
		nextCursor = next
		return sess, err
	}
	sess, err := hook.RunGetSessionHooks(hooks, hctx, final)
	if err != nil {
		return nil, "", err
	}
	if reads > 1 || (reads == 0 && sess != nil) {
		return nil, "", fmt.Errorf("%w: read policy returned an unpositioned snapshot", session.ErrTrackCursorInvalid)
	}
	if reads == 0 {
		nextCursor = cursor
	}
	if sess == nil {
		return &session.TrackEvents{Track: track}, nextCursor, nil
	}
	events, err := sess.GetTrackEvents(track)
	if errors.Is(err, session.ErrTracksEmpty) || errors.Is(err, session.ErrTrackEventsNotFound) {
		return &session.TrackEvents{Track: track}, nextCursor, nil
	}
	return events, nextCursor, err
}
