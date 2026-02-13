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

	v1 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v1"
	v2 "trpc.group/trpc-go/trpc-agent-go/session/redis/internal/v2"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

// persistTrackEvent persists track event to the appropriate storage (V1 or V2).
// Uses same strict dual-write semantics as AppendEvent (per xxx.md).
func (s *Service) persistTrackEvent(ctx context.Context, ver string, key session.Key, trackEvent *session.TrackEvent) error {
	// Dual-write mode: strict dual-write based on session existence
	if s.needDualWrite() {
		return s.appendTrackEventWithStrictDualWrite(ctx, key, trackEvent)
	}

	// Fast path: use version tag
	if ver == v2.VersionV2 {
		return s.v2Client.AppendTrackEvent(ctx, key, trackEvent)
	} else if ver == v1.VersionV1 {
		return s.v1Client.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Slow path: no version tag, check storage
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		log.WarnfContext(ctx, "checkSessionExists failed: %v", err)
	}

	if v2Exists {
		return s.v2Client.AppendTrackEvent(ctx, key, trackEvent)
	}
	if s.legacyEnabled() && v1Exists {
		return s.v1Client.AppendTrackEvent(ctx, key, trackEvent)
	}

	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}

// appendTrackEventWithStrictDualWrite implements strict dual-write for track events.
// Same semantics as appendEventWithStrictDualWrite (per xxx.md).
func (s *Service) appendTrackEventWithStrictDualWrite(ctx context.Context, key session.Key, trackEvent *session.TrackEvent) error {
	// Check which storages have this session
	v1Exists, v2Exists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return fmt.Errorf("check session exists failed: %w", err)
	}

	// Case 1: Both exist - strict dual-write, both must succeed
	if v1Exists && v2Exists {
		if err := s.v2Client.AppendTrackEvent(ctx, key, trackEvent); err != nil {
			return fmt.Errorf("dual-write track to V2 failed: %w", err)
		}
		if err := s.v1Client.AppendTrackEvent(ctx, key, trackEvent); err != nil {
			log.ErrorfContext(ctx, "dual-write track partial failure: V2 succeeded but V1 failed: %v", err)
			return fmt.Errorf("dual-write track to V1 failed (V2 succeeded): %w", err)
		}
		return nil
	}

	// Case 2: Only V2 exists
	if v2Exists {
		log.WarnfContext(ctx, "dual-write mode but only V2 exists for session %s/%s/%s, writing track to V2 only",
			key.AppName, key.UserID, key.SessionID)
		return s.v2Client.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Case 3: Only V1 exists
	if v1Exists {
		log.WarnfContext(ctx, "dual-write mode but only V1 exists for session %s/%s/%s, writing track to V1 only",
			key.AppName, key.UserID, key.SessionID)
		return s.v1Client.AppendTrackEvent(ctx, key, trackEvent)
	}

	// Case 4: Neither exists
	return fmt.Errorf("session not found: %s/%s/%s", key.AppName, key.UserID, key.SessionID)
}
