//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

// This file exports internal methods for testing purposes only.
// These exports are only available in test builds.

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// addEvent is exported for testing - delegates to AppendEvent.
// Note: This version accepts session.Key directly for compatibility with existing tests.
func (s *Service) addEvent(ctx context.Context, key session.Key, e *event.Event) error {
	sess := &session.Session{
		AppName: key.AppName,
		UserID:  key.UserID,
		ID:      key.SessionID,
	}
	return s.AppendEvent(ctx, sess, e)
}

// getSession is exported for testing - delegates to GetSession.
// Note: This version uses the old signature (eventNum int, eventTime time.Time) for compatibility.
func (s *Service) getSession(ctx context.Context, key session.Key, eventNum int, eventTime time.Time) (*session.Session, error) {
	var opts []session.Option
	if eventNum > 0 {
		opts = append(opts, session.WithEventNum(eventNum))
	}
	if !eventTime.IsZero() {
		opts = append(opts, session.WithEventTime(eventTime))
	}
	return s.GetSession(ctx, key, opts...)
}

// deleteSessionState is exported for testing - delegates to DeleteSession.
func (s *Service) deleteSessionState(ctx context.Context, key session.Key) error {
	return s.DeleteSession(ctx, key)
}

// addTrackEvent is exported for testing - delegates to AppendTrackEvent.
// Note: This version accepts session.Key directly for compatibility with existing tests.
func (s *Service) addTrackEvent(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	sess := &session.Session{
		AppName: key.AppName,
		UserID:  key.UserID,
		ID:      key.SessionID,
	}
	return s.AppendTrackEvent(ctx, sess, trackEvent)
}
