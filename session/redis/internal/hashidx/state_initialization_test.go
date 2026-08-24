//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hashidx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestStateInitializationLeaseKeyUsesDigest(t *testing.T) {
	client := NewClient(nil, defaultConfig())
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	leaseKey := client.StateInitializationLeaseKey(key, "digest")
	require.Contains(t, leaseKey, "digest")
	require.Contains(t, leaseKey, "{user}")
	require.NotContains(t, leaseKey, "private-state")
}

func TestLoadSessionStateValue(t *testing.T) {
	mr, redisClient := setupMiniredis(t)
	client := NewClient(redisClient, defaultConfig())
	ctx := context.Background()

	t.Run("missing session", func(t *testing.T) {
		value, present, generation, exists, err := client.LoadSessionStateValue(
			ctx,
			stateInitializationHashIdxKey("missing"),
			"state",
		)
		require.NoError(t, err)
		require.Nil(t, value)
		require.False(t, present)
		require.Empty(t, generation)
		require.False(t, exists)
	})

	t.Run("loads caller owned value", func(t *testing.T) {
		key := stateInitializationHashIdxKey("value")
		_, err := client.CreateSession(ctx, key, session.StateMap{
			"state": []byte("stored"),
		})
		require.NoError(t, err)
		value, present, generation, exists, err := client.LoadSessionStateValue(
			ctx,
			key,
			"state",
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.True(t, present)
		require.NotEmpty(t, generation)
		require.Equal(t, "stored", string(value))
		value[0] = 'X'

		reloaded, _, _, _, err := client.LoadSessionStateValue(ctx, key, "state")
		require.NoError(t, err)
		require.Equal(t, "stored", string(reloaded))
	})

	t.Run("migrates legacy generation and preserves ttl", func(t *testing.T) {
		key := stateInitializationHashIdxKey("legacy")
		meta := sessionMeta{
			ID:      key.SessionID,
			AppName: key.AppName,
			UserID:  key.UserID,
			State:   session.StateMap{"state": []byte("legacy")},
		}
		encoded, err := json.Marshal(meta)
		require.NoError(t, err)
		metaKey := client.keys.SessionMetaKey(key)
		require.NoError(t, redisClient.Set(ctx, metaKey, encoded, 5*time.Minute).Err())

		value, present, generation, exists, err := client.LoadSessionStateValue(
			ctx,
			key,
			"state",
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.True(t, present)
		require.Equal(t, "legacy", string(value))
		require.NotEmpty(t, generation)
		require.Positive(t, mr.TTL(metaKey))

		stored, err := redisClient.Get(ctx, metaKey).Bytes()
		require.NoError(t, err)
		var migrated sessionMeta
		require.NoError(t, json.Unmarshal(stored, &migrated))
		require.Equal(t, generation, migrated.Generation)
		for key := range migrated.State {
			require.False(t, strings.HasPrefix(key, "__TRPC_AGENT_GO_STATE_GENERATION_"))
		}
	})

	t.Run("rejects malformed records", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			raw    string
			exists bool
		}{
			{name: "json", raw: "{"},
			{name: "generation", raw: `{"state":{},"generation":1}`},
			{
				name:   "state",
				raw:    `{"state":"invalid","generation":"generation"}`,
				exists: true,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				key := stateInitializationHashIdxKey("bad-" + test.name)
				require.NoError(t, redisClient.Set(
					ctx,
					client.keys.SessionMetaKey(key),
					test.raw,
					0,
				).Err())
				_, _, _, exists, err := client.LoadSessionStateValue(ctx, key, "state")
				require.Error(t, err)
				require.Equal(t, test.exists, exists)
			})
		}
	})
}

func TestCommitStateInitialization(t *testing.T) {
	mr, redisClient := setupMiniredis(t)
	client := NewClient(redisClient, defaultConfig())
	ctx := context.Background()

	createSession := func(t *testing.T, id string) (session.Key, string) {
		t.Helper()
		key := stateInitializationHashIdxKey(id)
		_, err := client.CreateSession(ctx, key, nil)
		require.NoError(t, err)
		_, _, generation, exists, err := client.LoadSessionStateValue(ctx, key, "state")
		require.NoError(t, err)
		require.True(t, exists)
		return key, generation
	}
	setLease := func(t *testing.T, key session.Key, token string) string {
		t.Helper()
		leaseKey := client.StateInitializationLeaseKey(key, "digest")
		require.NoError(t, redisClient.Set(ctx, leaseKey, token, time.Minute).Err())
		return leaseKey
	}

	t.Run("commits value and releases lease", func(t *testing.T) {
		key, generation := createSession(t, "value")
		leaseKey := setLease(t, key, "owner")
		result, err := client.CommitStateInitialization(
			ctx, key, "state", []byte("value"), generation, leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Equal(t, 1, result)
		require.Zero(t, redisClient.Exists(ctx, leaseKey).Val())
		value, present, loadedGeneration, exists, err := client.LoadSessionStateValue(
			ctx, key, "state",
		)
		require.NoError(t, err)
		require.True(t, exists)
		require.True(t, present)
		require.Equal(t, generation, loadedGeneration)
		require.Equal(t, "value", string(value))
	})

	t.Run("preserves nil state value", func(t *testing.T) {
		key, generation := createSession(t, "nil")
		leaseKey := setLease(t, key, "owner")
		result, err := client.CommitStateInitialization(
			ctx, key, "state", nil, generation, leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Equal(t, 1, result)
		value, present, _, _, err := client.LoadSessionStateValue(ctx, key, "state")
		require.NoError(t, err)
		require.True(t, present)
		require.Nil(t, value)
	})

	t.Run("fences owner generation and missing session", func(t *testing.T) {
		key, generation := createSession(t, "fencing")
		leaseKey := setLease(t, key, "other")
		result, err := client.CommitStateInitialization(
			ctx, key, "state", []byte("value"), generation, leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Zero(t, result)
		require.Equal(t, "other", redisClient.Get(ctx, leaseKey).Val())

		leaseKey = setLease(t, key, "owner")
		result, err = client.CommitStateInitialization(
			ctx, key, "state", []byte("value"), "stale", leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Equal(t, -2, result)
		require.Zero(t, redisClient.Exists(ctx, leaseKey).Val())

		missing := stateInitializationHashIdxKey("missing-commit")
		leaseKey = setLease(t, missing, "owner")
		result, err = client.CommitStateInitialization(
			ctx, missing, "state", []byte("value"), "generation", leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Equal(t, -1, result)
		require.Zero(t, redisClient.Exists(ctx, leaseKey).Val())
	})

	t.Run("supports sub millisecond and zero ttl", func(t *testing.T) {
		for _, test := range []struct {
			name string
			ttl  time.Duration
		}{
			{name: "sub millisecond", ttl: time.Nanosecond},
			{name: "zero", ttl: 0},
		} {
			t.Run(test.name, func(t *testing.T) {
				key, generation := createSession(t, "ttl-"+test.name)
				metaKey := client.keys.SessionMetaKey(key)
				ttlBefore := mr.TTL(metaKey)
				leaseKey := setLease(t, key, "owner")
				cfg := defaultConfig()
				cfg.SessionTTL = test.ttl
				ttlClient := NewClient(redisClient, cfg)
				result, err := ttlClient.CommitStateInitialization(
					ctx, key, "state", []byte("value"), generation, leaseKey, "owner",
				)
				require.NoError(t, err)
				require.Equal(t, 1, result)
				if test.ttl > 0 {
					require.Equal(t, time.Millisecond, mr.TTL(metaKey))
				} else {
					require.Equal(t, ttlBefore, mr.TTL(metaKey))
				}
			})
		}
	})

	t.Run("clamps a zero millisecond remaining ttl", func(t *testing.T) {
		key, generation := createSession(t, "near-expiry")
		metaKey := client.keys.SessionMetaKey(key)
		mr.SetTTL(metaKey, time.Nanosecond)
		remainingTTL, err := redisClient.PTTL(ctx, metaKey).Result()
		require.NoError(t, err)
		require.Zero(t, remainingTTL)
		leaseKey := setLease(t, key, "owner")
		cfg := defaultConfig()
		cfg.SessionTTL = 0
		ttlClient := NewClient(redisClient, cfg)
		result, err := ttlClient.CommitStateInitialization(
			ctx, key, "state", []byte("value"), generation, leaseKey, "owner",
		)
		require.NoError(t, err)
		require.Equal(t, 1, result)
		require.Equal(t, time.Millisecond, mr.TTL(metaKey))
	})

	t.Run("returns script error", func(t *testing.T) {
		key := stateInitializationHashIdxKey("bad-commit")
		metaKey := client.keys.SessionMetaKey(key)
		require.NoError(t, redisClient.Set(ctx, metaKey, "{", 0).Err())
		leaseKey := setLease(t, key, "owner")
		_, err := client.CommitStateInitialization(
			ctx, key, "state", []byte("value"), "generation", leaseKey, "owner",
		)
		require.ErrorContains(t, err, "commit state initialization")
		require.Zero(t, redisClient.Exists(ctx, leaseKey).Val())
	})
}

func TestStateInitializationRedisErrorsAndClone(t *testing.T) {
	_, redisClient := setupMiniredis(t)
	client := NewClient(redisClient, defaultConfig())
	require.NoError(t, redisClient.Close())
	key := stateInitializationHashIdxKey("closed")
	_, _, _, exists, err := client.LoadSessionStateValue(context.Background(), key, "state")
	require.ErrorContains(t, err, "load session state value")
	require.False(t, exists)
	_, err = client.CommitStateInitialization(
		context.Background(), key, "state", []byte("value"), "generation", "lease", "owner",
	)
	require.ErrorContains(t, err, "commit state initialization")

	require.Nil(t, cloneStateInitializationValue(nil))
	original := []byte("value")
	cloned := cloneStateInitializationValue(original)
	cloned[0] = 'X'
	require.Equal(t, "value", string(original))
}

func stateInitializationHashIdxKey(id string) session.Key {
	return session.Key{AppName: "app", UserID: "user", SessionID: id}
}
