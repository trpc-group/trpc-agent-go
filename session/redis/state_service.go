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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UpdateAppState updates the state by target scope and key.
// Note: AppState key is shared between zset and hashidx (no v2 prefix), so no version routing needed.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	// AppState key is the same for both, so either client works
	return s.hashidxClient.UpdateAppState(ctx, appName, state, s.opts.appStateTTL)
}

// ListAppStates gets the app states.
// Note: AppState key is shared between zset and hashidx (no v2 prefix).
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	// AppState key is the same for both, so either client works
	return s.hashidxClient.ListAppStates(ctx, appName)
}

// DeleteAppState deletes the state by target scope and key.
// Note: AppState key is shared between zset and hashidx (no v2 prefix).
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}
	return s.hashidxClient.DeleteAppState(ctx, appName, key)
}

// UpdateUserState updates the state by target scope and key.
// Note: UserState keys are different between zset and hashidx (hashidx uses hashidx: prefix and different hash tag).
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// transition mode: write to both hashidx and zset
	if s.transitionEnabled() {
		if err := s.hashidxClient.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL); err != nil {
			return fmt.Errorf("update user state to hash idx failed: %w", err)
		}
		if err := s.zsetClient.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL); err != nil {
			return fmt.Errorf("update user stateo zset failed: %w", err)
		}
		return nil
	}

	// Legacy or None mode: write to hashidx only
	return s.hashidxClient.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL)
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	// Try hashidx first
	states, err := s.hashidxClient.ListUserStates(ctx, userKey)
	if err != nil {
		return nil, err
	}
	if len(states) > 0 {
		return states, nil
	}
	// Fallback to zset (if zset awareness is enabled: transition or legacy)
	if s.compatEnabled() {
		return s.zsetClient.ListUserStates(ctx, userKey)
	}
	return states, nil
}

// DeleteUserState deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}

	var errhashidx, errzset error
	// Delete from hashidx
	errhashidx = s.hashidxClient.DeleteUserState(ctx, userKey, key)
	// Also delete from zset (if zset awareness is enabled: transition or legacy)
	if s.compatEnabled() {
		errzset = s.zsetClient.DeleteUserState(ctx, userKey, key)
	}

	if errzset != nil {
		return fmt.Errorf("delete user state from zset failed: %w", errzset)
	}

	if errhashidx != nil {
		return fmt.Errorf("delete user state from hashidx failed: %w", errhashidx)
	}

	return nil
}

// UpdateSessionState updates the session-level state directly without appending an event.
func (s *Service) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	// Validate: disallow app: and user: prefixes
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("redis session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("redis session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	// Check session existence in zset and hashidx
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return fmt.Errorf("check session existence failed: %w", err)
	}

	// zset first: if zset exists, route to zset.
	if s.compatEnabled() && zsetExists {
		return s.zsetClient.UpdateSessionState(ctx, key, state)
	}
	if hashidxExists {
		return s.hashidxClient.UpdateSessionState(ctx, key, state)
	}

	return fmt.Errorf("session not found")
}
