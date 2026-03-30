//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UpdateSessionState updates the session-level state directly (HashIdx).
func (c *Client) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	ttlSeconds := int64(0)
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	statePatch := make(session.StateMap, len(state))
	nilKeys := make([]string, 0)
	for k, v := range state {
		if v == nil {
			nilKeys = append(nilKeys, k)
			continue
		}
		copiedValue := make([]byte, len(v))
		copy(copiedValue, v)
		statePatch[k] = copiedValue
	}

	statePatchJSON, err := json.Marshal(statePatch)
	if err != nil {
		return fmt.Errorf("marshal session state patch: %w", err)
	}
	nilKeysJSON, err := json.Marshal(nilKeys)
	if err != nil {
		return fmt.Errorf("marshal session state nil keys: %w", err)
	}

	result, err := luaUpdateSessionState.Run(
		ctx,
		c.client,
		[]string{c.keys.SessionMetaKey(key)},
		string(statePatchJSON),
		string(nilKeysJSON),
		time.Now().UTC().Format(time.RFC3339Nano),
		ttlSeconds,
	).Int()
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("session not found")
	}
	if result != 1 {
		return fmt.Errorf("update session state: unexpected script result %d", result)
	}
	return nil
}

// Exists checks if session exists.
func (c *Client) Exists(ctx context.Context, key session.Key) (bool, error) {
	n, err := c.client.Exists(ctx, c.keys.SessionMetaKey(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ExistsPipelined adds a HashIdx session existence check to the pipeline.
// Returns the IntCmd that can be evaluated after pipeline execution.
func (c *Client) ExistsPipelined(ctx context.Context, pipe redis.Pipeliner, key session.Key) *redis.IntCmd {
	return pipe.Exists(ctx, c.keys.SessionMetaKey(key))
}

// UpdateAppState updates app state.
func (c *Client) UpdateAppState(ctx context.Context, appName string, state session.StateMap, ttl time.Duration) error {
	key := c.keys.AppStateKey(appName)
	pipe := c.client.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteAppState deletes app state key.
func (c *Client) DeleteAppState(ctx context.Context, appName string, key string) error {
	return c.client.HDel(ctx, c.keys.AppStateKey(appName), key).Err()
}

// ListAppStates lists app states.
func (c *Client) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	res, err := c.client.HGetAll(ctx, c.keys.AppStateKey(appName)).Result()
	if err != nil {
		if err == redis.Nil {
			return make(session.StateMap), nil
		}
		return nil, err
	}
	state := make(session.StateMap)
	for k, v := range res {
		state[k] = []byte(v)
	}
	return state, nil
}

// UpdateUserState updates user state.
func (c *Client) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap, ttl time.Duration) error {
	key := c.keys.UserStateKey(userKey.AppName, userKey.UserID)
	pipe := c.client.TxPipeline()
	for k, v := range state {
		pipe.HSet(ctx, key, k, v)
	}
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// DeleteUserState deletes user state key.
func (c *Client) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return c.client.HDel(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID), key).Err()
}

// ListUserStates lists user states.
func (c *Client) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	res, err := c.client.HGetAll(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID)).Result()
	if err != nil {
		if err == redis.Nil {
			return make(session.StateMap), nil
		}
		return nil, err
	}
	state := make(session.StateMap)
	for k, v := range res {
		state[k] = []byte(v)
	}
	return state, nil
}

// RefreshAppStateTTL refreshes the TTL for app state key.
func (c *Client) RefreshAppStateTTL(ctx context.Context, appName string) error {
	if c.cfg.AppStateTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.AppStateKey(appName), c.cfg.AppStateTTL).Err()
}

// RefreshUserStateTTL refreshes the TTL for user state key.
func (c *Client) RefreshUserStateTTL(ctx context.Context, userKey session.UserKey) error {
	if c.cfg.UserStateTTL <= 0 {
		return nil
	}
	return c.client.Expire(ctx, c.keys.UserStateKey(userKey.AppName, userKey.UserID), c.cfg.UserStateTTL).Err()
}
