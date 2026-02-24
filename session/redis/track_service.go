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
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Update in-memory session first
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	// Async persist if enabled
	if s.opts.enableAsyncPersist {
		return s.enqueueTrackEvent(ctx, sess, key, trackEvent)
	}

	// Sync persist - route based on session version
	return s.persistTrackEvent(ctx, getSessionVersion(sess), key, trackEvent)
}

// enqueueTrackEvent enqueues a track event for async persistence.
func (s *Service) enqueueTrackEvent(ctx context.Context, sess *session.Session, key session.Key, trackEvent *session.TrackEvent) error {
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
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: trackEvent, version: ver}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// persistTrackEvent persists track event to the appropriate storage (zset or hashidx).
// Uses same strict dual-write semantics as AppendEvent (per xxx.md).
func (s *Service) persistTrackEvent(ctx context.Context, ver string, key session.Key, trackEvent *session.TrackEvent) error {
	// Dual-write mode: strict dual-write based on session existence
	if s.dualWriteEnabled() {
		return s.appendTrackEventWithStrictDualWrite(ctx, key, trackEvent)
	}

	// Fast path: use version tag
	switch ver {
	case util.StorageTypeHashIdx:
		return s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent)
	case util.StorageTypeZset:
		return s.zsetClient.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Slow path: no version tag, check storage.
	// zset first: if zset exists, it's a legacy session.
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
	}

	if s.compatEnabled() && zsetExists {
		return s.zsetClient.AppendTrackEvent(ctx, key, trackEvent)
	}
	if hashidxExists {
		return s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent)
	}

	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}

// appendTrackEventWithStrictDualWrite implements strict dual-write for track events.
// Same semantics as appendEventWithStrictDualWrite (per xxx.md).
func (s *Service) appendTrackEventWithStrictDualWrite(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	// Check which storages have this session
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return fmt.Errorf("check session exists failed: %w", err)
	}

	// Case 1: Both exist - strict dual-write, both must succeed
	if zsetExists && hashidxExists {
		if err := s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent); err != nil {
			return fmt.Errorf("dual-write track to hashidx failed: %w", err)
		}
		if err := s.zsetClient.AppendTrackEvent(ctx, key, trackEvent); err != nil {
			log.ErrorfContext(ctx, "dual-write track partial failure: hashidx succeeded but zset failed: %v", err)
			return fmt.Errorf("dual-write track to zset failed (hashidx succeeded): %w", err)
		}
		return nil
	}

	// Case 2: Only hashidx exists
	if hashidxExists {
		log.WarnfContext(ctx, "dual-write mode but only hashidx exists for session %s/%s/%s, writing track to hashidx only",
			key.AppName, key.UserID, key.SessionID)
		return s.hashidxClient.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Case 3: Only zset exists
	if zsetExists {
		log.WarnfContext(ctx, "dual-write mode but only zset exists for session %s/%s/%s, writing track to zset only",
			key.AppName, key.UserID, key.SessionID)
		return s.zsetClient.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Case 4: Neither exists
	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}
