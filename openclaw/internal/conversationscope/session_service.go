//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationscope

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type sessionService struct {
	next session.Service
}

// WrapSessionService rewrites persisted session keys using any explicit
// per-request storage user scope carried on the context.
func WrapSessionService(next session.Service) session.Service {
	if next == nil {
		return nil
	}
	return &sessionService{next: next}
}

func (s *sessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	storageKey := rewriteKeyForStorage(ctx, key)
	sess, err := s.next.CreateSession(ctx, storageKey, state, options...)
	if err != nil || sess == nil {
		return sess, err
	}
	if err := RememberIndexedStorageUser(
		ctx,
		s.next,
		key.AppName,
		key.UserID,
		storageKey.UserID,
	); err != nil {
		return nil, fmt.Errorf(
			"remember indexed storage user for create session: %w",
			err,
		)
	}
	if err := RememberIndexedStorageScope(
		ctx,
		s.next,
		key.AppName,
		storageKey.UserID,
	); err != nil {
		log.Warnf(
			"conversation scope: skip create-session scope index "+
				"%q/%q: %v",
			key.AppName,
			storageKey.UserID,
			err,
		)
	}
	return rewriteSessionForUser(sess, key.UserID), nil
}

func (s *sessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	storageKey := rewriteKeyForStorage(ctx, key)
	sess, err := s.next.GetSession(ctx, storageKey, options...)
	if err != nil || sess == nil {
		return sess, err
	}
	if err := RememberIndexedStorageUser(
		ctx,
		s.next,
		key.AppName,
		key.UserID,
		storageKey.UserID,
	); err != nil {
		return nil, fmt.Errorf(
			"remember indexed storage user for get session: %w",
			err,
		)
	}
	if err := RememberIndexedStorageScope(
		ctx,
		s.next,
		key.AppName,
		storageKey.UserID,
	); err != nil {
		log.Warnf(
			"conversation scope: skip get-session scope index "+
				"%q/%q: %v",
			key.AppName,
			storageKey.UserID,
			err,
		)
	}
	return rewriteSessionForUser(sess, key.UserID), nil
}

func (s *sessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	storageUserKey := userKey
	storageUserKey.UserID = StorageUserIDFromContext(ctx, userKey.UserID)
	sessions, err := s.next.ListSessions(ctx, storageUserKey, options...)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i] = rewriteSessionForUser(sessions[i], userKey.UserID)
	}
	return sessions, nil
}

func (s *sessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	return s.next.DeleteSession(ctx, rewriteKeyForStorage(ctx, key), options...)
}

func (s *sessionService) UpdateAppState(
	ctx context.Context,
	appName string,
	state session.StateMap,
) error {
	return s.next.UpdateAppState(ctx, appName, state)
}

func (s *sessionService) DeleteAppState(
	ctx context.Context,
	appName string,
	key string,
) error {
	return s.next.DeleteAppState(ctx, appName, key)
}

func (s *sessionService) ListAppStates(
	ctx context.Context,
	appName string,
) (session.StateMap, error) {
	return s.next.ListAppStates(ctx, appName)
}

func (s *sessionService) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	return s.next.UpdateUserState(ctx, userKey, state)
}

func (s *sessionService) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	return s.next.ListUserStates(ctx, userKey)
}

func (s *sessionService) DeleteUserState(
	ctx context.Context,
	userKey session.UserKey,
	key string,
) error {
	return s.next.DeleteUserState(ctx, userKey, key)
}

func (s *sessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	return s.next.UpdateSessionState(ctx, rewriteKeyForStorage(ctx, key), state)
}

func (s *sessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	return s.withStorageSessionRuntimeSync(
		ctx,
		sess,
		func(storageSess *session.Session) error {
			return s.next.AppendEvent(
				ctx,
				storageSess,
				evt,
				options...,
			)
		},
	)
}

func (s *sessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return s.withStorageSessionRuntimeSync(
		ctx,
		sess,
		func(storageSess *session.Session) error {
			return s.next.CreateSessionSummary(
				ctx,
				storageSess,
				filterKey,
				force,
			)
		},
	)
}

func (s *sessionService) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return s.withStorageSessionRuntimeSync(
		ctx,
		sess,
		func(storageSess *session.Session) error {
			return s.next.EnqueueSummaryJob(
				ctx,
				storageSess,
				filterKey,
				force,
			)
		},
	)
}

func (s *sessionService) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	return s.next.GetSessionSummaryText(
		ctx,
		rewriteSessionForStorage(ctx, sess),
		opts...,
	)
}

func (s *sessionService) Close() error {
	return s.next.Close()
}

func rewriteKeyForStorage(
	ctx context.Context,
	key session.Key,
) session.Key {
	key.UserID = StorageUserIDFromContext(ctx, key.UserID)
	return key
}

func rewriteSessionForStorage(
	ctx context.Context,
	sess *session.Session,
) *session.Session {
	if sess == nil {
		return nil
	}
	storageUserID := StorageUserIDFromContext(ctx, sess.UserID)
	if storageUserID == sess.UserID {
		return sess
	}
	return rewriteSessionForUser(sess, storageUserID)
}

func rewriteSessionForUser(
	sess *session.Session,
	userID string,
) *session.Session {
	if sess == nil {
		return nil
	}
	cloned := sess.Clone()
	cloned.UserID = userID
	return cloned
}

// withStorageSessionRuntimeSync keeps the caller's canonical session runtime
// state aligned with backend-visible mutations applied to a storage-scoped
// clone. This preserves canonical UserID semantics while avoiding split-brain
// state during the current invocation.
func (s *sessionService) withStorageSessionRuntimeSync(
	ctx context.Context,
	sess *session.Session,
	apply func(*session.Session) error,
) error {
	storageSess := rewriteSessionForStorage(ctx, sess)
	if err := apply(storageSess); err != nil {
		return err
	}
	syncSessionRuntimeState(sess, storageSess)
	return nil
}

func syncSessionRuntimeState(
	dst *session.Session,
	src *session.Session,
) {
	if dst == nil || src == nil || dst == src {
		return
	}

	snapshot := src.Clone()

	dst.EventMu.Lock()
	dst.Events = snapshot.Events
	dst.EventMu.Unlock()

	session.WithSessionState(snapshot.State)(dst)

	dst.TracksMu.Lock()
	dst.Tracks = snapshot.Tracks
	dst.TracksMu.Unlock()

	dst.SummariesMu.Lock()
	dst.Summaries = snapshot.Summaries
	dst.SummariesMu.Unlock()

	dst.ServiceMeta = snapshot.ServiceMeta
	dst.UpdatedAt = snapshot.UpdatedAt
}
