//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package noop provides a session service that keeps no persisted state.
package noop

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	_ session.Service      = (*Service)(nil)
	_ session.TrackService = (*Service)(nil)
)

// Service implements session.Service without storing sessions or state.
type Service struct{}

// NewService creates a new no-op session service.
func NewService() *Service {
	return &Service{}
}

// CreateSession creates a transient session and does not persist it.
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	if key.SessionID == "" {
		key.SessionID = uuid.NewString()
	}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	for k, v := range state {
		sess.SetState(k, v)
	}
	return sess, nil
}

// GetSession always returns nil after validating the key and options.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	if err := session.ValidateGetSessionOptions(opt, false); err != nil {
		return nil, err
	}
	return nil, nil
}

// ListSessions always returns an empty list after validating the key and options.
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	if err := session.ValidateListSessionsOptions(opt); err != nil {
		return nil, err
	}
	return []*session.Session{}, nil
}

// DeleteSession validates the key and does not persist any deletion.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) error {
	return key.CheckSessionKey()
}

// UpdateAppState validates the app name and drops the state update.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return nil
}

// DeleteAppState validates the app name and drops the delete request.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return nil
}

// ListAppStates validates the app name and returns an empty state map.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	return session.StateMap{}, nil
}

// UpdateUserState validates the user key and drops the state update.
func (s *Service) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return nil
}

// ListUserStates validates the user key and returns an empty state map.
func (s *Service) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	return session.StateMap{}, nil
}

// DeleteUserState validates the user key and drops the delete request.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return userKey.CheckUserKey()
}

// UpdateSessionState validates the session key and drops the state update.
func (s *Service) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	return nil
}

// AppendEvent updates the transient session object and does not persist the event.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	sess.UpdateUserSession(evt, opts...)
	return nil
}

// AppendTrackEvent updates the transient session object and does not persist the event.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	return sess.AppendTrackEvent(trackEvent, opts...)
}

// CreateSessionSummary is a no-op.
func (s *Service) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	return key.CheckSessionKey()
}

// EnqueueSummaryJob is a no-op.
func (s *Service) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	return key.CheckSessionKey()
}

// GetSessionSummaryText always reports that no summary exists.
func (s *Service) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	return "", false
}

// Close closes the no-op service.
func (s *Service) Close() error {
	return nil
}

func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}
