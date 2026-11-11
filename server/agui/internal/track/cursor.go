//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ErrCursorUnsupported is only returned after a legacy read has no cursor metadata.
// Backend and policy errors must never trigger a second, less restrictive read.
var ErrCursorUnsupported = errors.New("track cursors are not supported")

// GetEventsWithCursor uses the optional storage continuation capability.
func (t *tracker) GetEventsWithCursor(ctx context.Context, key session.Key, cursor string) (*session.TrackEvents, string, error) {
	if _, ok := t.sessionService.(trackEventReader); !ok {
		events, err := t.GetEvents(ctx, key)
		if err != nil {
			return nil, "", err
		}
		return events, "", ErrCursorUnsupported
	}
	events, err := t.GetEvents(ctx, key, session.WithTrackCursor(cursor))
	if err != nil {
		return nil, "", err
	}
	if events == nil {
		events = &session.TrackEvents{Track: TrackAGUI}
	}
	if events.Cursor == nil {
		return events, "", ErrCursorUnsupported
	}
	return events, *events.Cursor, nil
}
