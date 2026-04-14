//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// AppendTrackEvent appends a protocol-specific track event to a session.
// Strategy: Track event storage version follows session storage version.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	ctx, span := s.startSpan(ctx, "append_track_event", key)
	defer span.End()

	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Update in-memory session first
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	// Snapshot the tracks state for persistence (sess.State["tracks"] is updated by AppendTrackEvent)
	tracksState := sess.SnapshotTracksState()

	// Async persist if enabled
	if s.opts.enableAsyncPersist {
		return s.enqueueTrackEvent(ctx, sess, key, trackEvent, tracksState)
	}

	// Sync persist - route based on session version
	return s.persistTrackEvent(ctx, getSessionVersion(sess), key, trackEvent, tracksState)
}

// enqueueTrackEvent enqueues a track event for async persistence.
func (s *Service) enqueueTrackEvent(ctx context.Context, sess *session.Session, key session.Key, trackEvent *session.TrackEvent, tracksState []byte) error {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok && err.Error() == "send on closed channel" {
				log.ErrorfContext(ctx, "redis session service append track event failed: %v", r)
				return
			}
			panic(r)
		}
	}()

	ver := getSessionVersion(sess)
	index := sess.Hash % len(s.trackEventChans)
	select {
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: trackEvent, version: ver, tracksState: tracksState}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// persistTrackEvent persists track event to the appropriate storage (zset or hashidx).
func (s *Service) persistTrackEvent(ctx context.Context, ver string, key session.Key, trackEvent *session.TrackEvent, tracksState []byte) error {
	// Fast path: use version tag
	switch ver {
	case util.StorageTypeHashIdx:
		s.recordStorageRoute(ctx, opAppendTrackEvent, util.StorageTypeHashIdx)
		return s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent, tracksState)
	case util.StorageTypeZset:
		s.recordStorageRoute(ctx, opAppendTrackEvent, util.StorageTypeZset)
		return s.zsetClient.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Slow path: no version tag, check storage.
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
	}

	if s.compatEnabled() && zsetExists {
		s.recordStorageRoute(ctx, opAppendTrackEvent, util.StorageTypeZset)
		return s.zsetClient.AppendTrackEvent(ctx, key, trackEvent)
	}
	if hashidxExists {
		s.recordStorageRoute(ctx, opAppendTrackEvent, util.StorageTypeHashIdx)
		return s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent, tracksState)
	}

	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}
