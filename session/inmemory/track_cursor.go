//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"bytes"
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackcursor"
)

// readTrackEvents reads a track using a session-bound append position.
// Session replacement or expiration invalidates an existing cursor.
func (s *SessionService) readTrackEvents(ctx context.Context, key session.Key, track session.Track,
	cursor string) (*session.TrackEvents, string, error) {
	return trackcursor.Read(ctx, key, track, cursor, s.opts.getSessionHooks,
		func(c *session.GetSessionContext) (*session.Session, string, error) {
			position, err := trackcursor.Decode(cursor, "inmemory", c.Key, track)
			if err != nil {
				return nil, "", err
			}
			sess, err := s.getSessionView(c.Context, c.Key, c.Options,
				func(stored *sessionWithTTL, snapshot *session.Session) error {
					if stored.trackGeneration == "" {
						stored.trackGeneration = uuid.NewString()
					}
					if position.Generation != "" && position.Generation != stored.trackGeneration {
						return session.ErrTrackCursorInvalid
					}
					position.Generation = stored.trackGeneration
					var count uint64
					if events := snapshot.Tracks[track]; events != nil {
						count = uint64(len(events.Events))
						if position.Position > count {
							return session.ErrTrackCursorInvalid
						}
						events.Events = events.Events[position.Position:]
						for i := range events.Events {
							events.Events[i].Payload = bytes.Clone(events.Events[i].Payload)
						}
					} else if position.Position != 0 {
						return session.ErrTrackCursorInvalid
					}
					position.Position = count
					return nil
				})
			if err != nil {
				return nil, "", err
			}
			if sess == nil && position.Generation != "" {
				return nil, "", session.ErrTrackCursorInvalid
			}
			return sess, trackcursor.Encode(position), nil
		})
}
