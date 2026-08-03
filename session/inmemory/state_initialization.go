//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

var errStateInitializationClosed = errors.New(
	"memory session service state initialization is closed",
)

type stateInitializationKey struct {
	sessionKey session.Key
	stateKey   string
}

type stateInitializationGate struct {
	done chan struct{}
	once sync.Once
}

func newStateInitializationGate() *stateInitializationGate {
	return &stateInitializationGate{done: make(chan struct{})}
}

func (g *stateInitializationGate) release() {
	if g == nil {
		return
	}
	g.once.Do(func() { close(g.done) })
}

// LoadOrInitializeSessionState implements session.StateInitializationService.
func (s *SessionService) LoadOrInitializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
) ([]byte, bool, error) {
	if err := validateStateInitializationArguments(
		key,
		stateKey,
		validate,
		initialize,
	); err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	coordinationKey := stateInitializationKey{
		sessionKey: key,
		stateKey:   stateKey,
	}
	value, present, expectedGeneration, err := s.loadSessionStateValue(
		key,
		stateKey,
	)
	if err != nil {
		return nil, false, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if present && validate(cloneStateInitializationValue(value)) {
			return cloneStateInitializationValue(value), false, nil
		}

		gate, owner, err := s.acquireStateInitializationGate(coordinationKey)
		if err != nil {
			return nil, false, err
		}
		if owner {
			return s.initializeSessionState(
				ctx,
				key,
				stateKey,
				validate,
				initialize,
				expectedGeneration,
				coordinationKey,
				gate,
			)
		}

		select {
		case <-gate.done:
		case <-s.stateInitializationClosed:
			return nil, false, errStateInitializationClosed
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
		var generation *sessionWithTTL
		value, present, generation, err = s.loadSessionStateValue(key, stateKey)
		if err != nil {
			return nil, false, err
		}
		if generation != expectedGeneration {
			return nil, false, errors.New(
				"memory session service initialize session state failed: session generation changed",
			)
		}
	}
}

func validateStateInitializationArguments(
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if strings.TrimSpace(stateKey) == "" {
		return errors.New("state key is required")
	}
	if strings.HasPrefix(stateKey, session.StateAppPrefix) {
		return fmt.Errorf(
			"memory session service initialize session state failed: %s is not allowed, use UpdateAppState instead",
			stateKey,
		)
	}
	if strings.HasPrefix(stateKey, session.StateUserPrefix) {
		return fmt.Errorf(
			"memory session service initialize session state failed: %s is not allowed, use UpdateUserState instead",
			stateKey,
		)
	}
	if validate == nil {
		return errors.New("state validation function is required")
	}
	if initialize == nil {
		return errors.New("state initialization function is required")
	}
	return nil
}

func (s *SessionService) initializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
	expectedGeneration *sessionWithTTL,
	coordinationKey stateInitializationKey,
	gate *stateInitializationGate,
) ([]byte, bool, error) {
	defer s.releaseStateInitializationGate(coordinationKey, gate)

	value, present, generation, err := s.loadSessionStateValue(key, stateKey)
	if err != nil {
		return nil, false, err
	}
	if generation != expectedGeneration {
		return nil, false, errors.New(
			"memory session service initialize session state failed: session generation changed",
		)
	}
	if present && validate(cloneStateInitializationValue(value)) {
		return cloneStateInitializationValue(value), false, nil
	}

	initializeCtx, cancelInitialize := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		select {
		case <-s.stateInitializationClosed:
			cancelInitialize()
		case <-initializeCtx.Done():
		}
	}()
	defer func() {
		cancelInitialize()
		<-monitorDone
	}()

	value, err = initialize(initializeCtx)
	if err != nil {
		callbackErr := fmt.Errorf("initialize session state: %w", err)
		select {
		case <-s.stateInitializationClosed:
			return nil, false, errors.Join(
				errStateInitializationClosed,
				callbackErr,
			)
		default:
		}
		return nil, false, callbackErr
	}
	if err := initializeCtx.Err(); err != nil {
		select {
		case <-s.stateInitializationClosed:
			return nil, false, errStateInitializationClosed
		default:
		}
		return nil, false, err
	}
	value = cloneStateInitializationValue(value)
	if !validate(cloneStateInitializationValue(value)) {
		return nil, false, errors.New("initialize session state: callback returned an invalid value")
	}
	if err := s.commitInitializedSessionState(
		initializeCtx,
		key,
		stateKey,
		generation,
		value,
	); err != nil {
		return nil, false, err
	}
	return cloneStateInitializationValue(value), true, nil
}

func (s *SessionService) acquireStateInitializationGate(
	key stateInitializationKey,
) (*stateInitializationGate, bool, error) {
	s.stateInitializationMu.Lock()
	defer s.stateInitializationMu.Unlock()
	if s.stateInitializationClosed == nil {
		s.stateInitializationClosed = make(chan struct{})
	}
	if s.stateInitializationGates == nil {
		s.stateInitializationGates = make(
			map[stateInitializationKey]*stateInitializationGate,
		)
	}
	select {
	case <-s.stateInitializationClosed:
		return nil, false, errStateInitializationClosed
	default:
	}
	if gate := s.stateInitializationGates[key]; gate != nil {
		return gate, false, nil
	}
	gate := newStateInitializationGate()
	s.stateInitializationGates[key] = gate
	return gate, true, nil
}

func (s *SessionService) releaseStateInitializationGate(
	key stateInitializationKey,
	gate *stateInitializationGate,
) {
	s.stateInitializationMu.Lock()
	if s.stateInitializationGates[key] == gate {
		delete(s.stateInitializationGates, key)
	}
	s.stateInitializationMu.Unlock()
	gate.release()
}

func (s *SessionService) closeStateInitialization() {
	s.stateInitializationMu.Lock()
	if s.stateInitializationClosed == nil {
		s.stateInitializationClosed = make(chan struct{})
	}
	select {
	case <-s.stateInitializationClosed:
		s.stateInitializationMu.Unlock()
		return
	default:
		close(s.stateInitializationClosed)
	}
	gates := s.stateInitializationGates
	s.stateInitializationGates = make(
		map[stateInitializationKey]*stateInitializationGate,
	)
	s.stateInitializationMu.Unlock()
	for _, gate := range gates {
		gate.release()
	}
}

func (s *SessionService) loadSessionStateValue(
	key session.Key,
	stateKey string,
) ([]byte, bool, *sessionWithTTL, error) {
	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return nil, false, nil, errors.New("memory session service initialize session state failed: session not found")
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	userSessions := app.sessions[key.UserID]
	if userSessions == nil {
		return nil, false, nil, errors.New("memory session service initialize session state failed: session not found")
	}
	stored := userSessions[key.SessionID]
	if stored == nil {
		return nil, false, nil, errors.New("memory session service initialize session state failed: session not found")
	}
	if isExpired(stored.expiredAt) {
		return nil, false, nil, errors.New("memory session service initialize session state failed: session expired")
	}
	value, present := stored.session.GetState(stateKey)
	return value, present, stored, nil
}

func (s *SessionService) commitInitializedSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	generation *sessionWithTTL,
	value []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.stateInitializationMu.Lock()
	defer s.stateInitializationMu.Unlock()
	select {
	case <-s.stateInitializationClosed:
		return errStateInitializationClosed
	default:
	}

	app, ok := s.getAppSessions(key.AppName)
	if !ok {
		return errors.New("memory session service initialize session state failed: session not found")
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	userSessions := app.sessions[key.UserID]
	if userSessions == nil || userSessions[key.SessionID] == nil {
		return errors.New("memory session service initialize session state failed: session not found")
	}
	stored := userSessions[key.SessionID]
	if isExpired(stored.expiredAt) {
		return errors.New("memory session service initialize session state failed: session expired")
	}
	if stored != generation {
		return errors.New(
			"memory session service initialize session state failed: session generation changed",
		)
	}
	stored.session.SetState(stateKey, value)
	stored.session.UpdatedAt = time.Now()
	if s.opts.sessionTTL > 0 {
		stored.expiredAt = calculateExpiredAt(s.opts.sessionTTL)
	}
	return nil
}

func cloneStateInitializationValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
