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
// Note: AppState key is shared between V1 and V2 (no v2 prefix), so no dual-write needed.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	return s.v2Client.UpdateAppState(ctx, appName, state, s.opts.appStateTTL)
}

// ListAppStates gets the app states.
// Note: AppState key is shared between V1 and V2 (no v2 prefix).
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}
	return s.v2Client.ListAppStates(ctx, appName)
}

// DeleteAppState deletes the state by target scope and key.
// Note: AppState key is shared between V1 and V2 (no v2 prefix).
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}
	return s.v2Client.DeleteAppState(ctx, appName, key)
}

// UpdateUserState updates the state by target scope and key.
// Note: UserState keys are different between V1 and V2 (V2 uses v2: prefix and different hash tag).
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	// Dual-write mode: write to both V2 and V1
	if s.needDualWrite() {
		if err := s.v2Client.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL); err != nil {
			return err
		}
		if err := s.v1Client.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL); err != nil {
			return fmt.Errorf("dual-write user state to V1 failed: %w", err)
		}
		return nil
	}

	// Legacy or None mode: write to V2 only
	return s.v2Client.UpdateUserState(ctx, userKey, state, s.opts.userStateTTL)
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	// Try V2 first
	states, err := s.v2Client.ListUserStates(ctx, userKey)
	if err != nil {
		return nil, err
	}
	if len(states) > 0 {
		return states, nil
	}
	// Fallback to V1
	if s.legacyEnabled() {
		return s.v1Client.ListUserStates(ctx, userKey)
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
	// Delete from both V2 and V1
	errV2 := s.v2Client.DeleteUserState(ctx, userKey, key)
	if s.legacyEnabled() {
		errV1 := s.v1Client.DeleteUserState(ctx, userKey, key)
		if errV2 != nil {
			return errV2
		}
		return errV1
	}
	return errV2
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

	// Strategy: Check V2 Exists -> V2 Update. Else -> V1 Update.

	// Check V2 Exists
	v2Exists, err := s.v2Client.Exists(ctx, key)
	if err != nil {
		return fmt.Errorf("check session existence failed: %w", err)
	}

	if v2Exists {
		return s.v2Client.UpdateSessionState(ctx, key, state)
	}

	if s.legacyEnabled() {
		// V1 UpdateSessionState checks existence internally.
		return s.v1Client.UpdateSessionState(ctx, key, state)
	}

	return fmt.Errorf("session not found")
}
