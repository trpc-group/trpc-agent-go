//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	errAttemptAppStateWriteDisabled  = errors.New("candidate selector: app state writes are disabled in candidate attempts")
	errAttemptUserStateWriteDisabled = errors.New("candidate selector: user state writes are disabled in candidate attempts")
)

type attemptSessionService struct {
	base             session.Service
	mu               sync.Mutex
	sessions         map[session.Key]*session.Session
	deletedSessions  map[session.Key]bool
	directStateDelta session.StateMap
}

func newAttemptSessionService(
	base session.Service,
	root *session.Session,
) *attemptSessionService {
	s := &attemptSessionService{
		base:             base,
		sessions:         make(map[session.Key]*session.Session),
		deletedSessions:  make(map[session.Key]bool),
		directStateDelta: make(session.StateMap),
	}
	if root != nil {
		s.sessions[keyFromSession(root)] = root
	}
	return s
}

func (s *attemptSessionService) Service() session.Service {
	return s
}

func (s *attemptSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := session.NewSession(
		key.AppName,
		key.UserID,
		key.SessionID,
		session.WithSessionState(cloneStateMap(state)),
	)
	s.sessions[key] = sess
	delete(s.deletedSessions, key)
	return sess.Clone(), nil
}

func (s *attemptSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := &session.Options{}
	for _, o := range options {
		o(opt)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.getSessionLocked(ctx, key)
	if err != nil || sess == nil {
		return nil, err
	}
	copied := sess.Clone()
	copied.ApplyEventFiltering(
		session.WithEventNum(opt.EventNum),
		session.WithEventTime(opt.EventTime),
	)
	return copied, nil
}

func (s *attemptSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	list := make([]*session.Session, 0)
	if s.base != nil {
		baseList, err := s.base.ListSessions(ctx, userKey, options...)
		if err != nil {
			return nil, err
		}
		for _, sess := range baseList {
			if sess == nil {
				continue
			}
			list = append(list, sess.Clone())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[session.Key]int, len(list))
	filtered := list[:0]
	for _, sess := range list {
		if sess == nil {
			continue
		}
		key := keyFromSession(sess)
		if s.deletedSessions[key] {
			continue
		}
		seen[key] = len(filtered)
		filtered = append(filtered, sess)
	}
	list = filtered
	for key, sess := range s.sessions {
		if key.AppName != userKey.AppName || key.UserID != userKey.UserID {
			continue
		}
		if s.deletedSessions[key] {
			continue
		}
		if idx, ok := seen[key]; ok {
			list[idx] = sess.Clone()
			continue
		}
		list = append(list, sess.Clone())
	}
	return list, nil
}

func (s *attemptSessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
	s.deletedSessions[key] = true
	return nil
}

func (s *attemptSessionService) UpdateAppState(
	ctx context.Context,
	appName string,
	state session.StateMap,
) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return errAttemptAppStateWriteDisabled
}

func (s *attemptSessionService) DeleteAppState(
	ctx context.Context,
	appName string,
	key string,
) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return errAttemptAppStateWriteDisabled
}

func (s *attemptSessionService) ListAppStates(
	ctx context.Context,
	appName string,
) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	if s.base != nil {
		return s.base.ListAppStates(ctx, appName)
	}
	return make(session.StateMap), nil
}

func (s *attemptSessionService) UpdateUserState(
	ctx context.Context,
	userKey session.UserKey,
	state session.StateMap,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return errAttemptUserStateWriteDisabled
}

func (s *attemptSessionService) ListUserStates(
	ctx context.Context,
	userKey session.UserKey,
) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if s.base != nil {
		return s.base.ListUserStates(ctx, userKey)
	}
	return make(session.StateMap), nil
}

func (s *attemptSessionService) DeleteUserState(
	ctx context.Context,
	userKey session.UserKey,
	key string,
) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	return errAttemptUserStateWriteDisabled
}

func (s *attemptSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	for stateKey := range state {
		if strings.HasPrefix(stateKey, session.StateAppPrefix) {
			return fmt.Errorf("attempt session service update session state failed: %s is not allowed, use UpdateAppState instead", stateKey)
		}
		if strings.HasPrefix(stateKey, session.StateUserPrefix) {
			return fmt.Errorf("attempt session service update session state failed: %s is not allowed, use UpdateUserState instead", stateKey)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, err := s.getSessionLocked(ctx, key)
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("attempt session service update session state failed: session not found")
	}
	for stateKey, value := range state {
		sess.SetState(stateKey, value)
		s.directStateDelta[stateKey] = cloneBytes(value)
	}
	return nil
}

func (s *attemptSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := keyFromSession(sess)
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.getSessionLocked(ctx, key)
	if err != nil {
		return err
	}
	if stored == nil {
		stored = sess.Clone()
		s.sessions[key] = stored
	}
	if stored != sess {
		sess.UpdateUserSession(evt, options...)
	}
	stored.UpdateUserSession(evt, options...)
	if evt == nil {
		return nil
	}
	for stateKey := range evt.StateDelta {
		delete(s.directStateDelta, stateKey)
	}
	return nil
}

func (s *attemptSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return nil
}

func (s *attemptSessionService) EnqueueSummaryJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return nil
}

func (s *attemptSessionService) GetSessionSummaryText(
	ctx context.Context,
	sess *session.Session,
	opts ...session.SummaryOption,
) (string, bool) {
	if s.base == nil {
		return "", false
	}
	return s.base.GetSessionSummaryText(ctx, sess, opts...)
}

func (s *attemptSessionService) Close() error {
	return nil
}

func (s *attemptSessionService) DirectStateDelta() session.StateMap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStateMap(s.directStateDelta)
}

func (s *attemptSessionService) getSessionLocked(
	ctx context.Context,
	key session.Key,
) (*session.Session, error) {
	if s.deletedSessions[key] {
		return nil, nil
	}
	if sess, ok := s.sessions[key]; ok {
		return sess, nil
	}
	if s.base == nil {
		return nil, nil
	}
	baseSess, err := s.base.GetSession(ctx, key)
	if err != nil || baseSess == nil {
		return baseSess, err
	}
	cloned := baseSess.Clone()
	s.sessions[key] = cloned
	return cloned, nil
}

func keyFromSession(sess *session.Session) session.Key {
	if sess == nil {
		return session.Key{}
	}
	return session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
}

func cloneStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	cloned := make(session.StateMap, len(state))
	for key, value := range state {
		cloned[key] = cloneBytes(value)
	}
	return cloned
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
