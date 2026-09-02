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
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

func TestClient_CreateSummary(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum1"}

	t.Run("nil summary returns error", func(t *testing.T) {
		applied, err := c.CreateSummary(ctx, key, "all", nil, time.Hour)
		require.Error(t, err)
		assert.ErrorContains(t, err, summaryNilError)
		assert.False(t, applied.Applied())
	})

	t.Run("creates new summary", func(t *testing.T) {
		now := time.Now()
		sum := &session.Summary{Summary: "test summary", UpdatedAt: now}
		applied, err := c.CreateSummary(ctx, key, "all", sum, time.Hour)
		require.NoError(t, err)
		assert.True(t, applied.Applied(), "a first write must report applied")

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "test summary", result["all"].Summary)
	})

	t.Run("updates with newer timestamp", func(t *testing.T) {
		newer := time.Now().Add(time.Hour)
		sum := &session.Summary{Summary: "updated", UpdatedAt: newer}
		applied, err := c.CreateSummary(ctx, key, "all", sum, time.Hour)
		require.NoError(t, err)
		assert.True(t, applied.Applied(), "a newer summary must report applied")

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "updated", result["all"].Summary)
	})

	t.Run("skips older timestamp", func(t *testing.T) {
		older := time.Now().Add(-24 * time.Hour)
		sum := &session.Summary{Summary: "old", UpdatedAt: older}
		applied, err := c.CreateSummary(ctx, key, "all", sum, time.Hour)
		require.NoError(t, err)
		assert.False(t, applied.Applied(),
			"a skipped stale write must not report applied")

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "updated", result["all"].Summary)
	})

	t.Run("multiple filter keys", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "sum2"}
		now := time.Now()

		applied, err := c.CreateSummary(
			ctx, key2, "all", &session.Summary{Summary: "all-sum", UpdatedAt: now}, time.Hour)
		require.NoError(t, err)
		assert.True(t, applied.Applied())
		applied, err = c.CreateSummary(
			ctx, key2, "custom", &session.Summary{Summary: "custom-sum", UpdatedAt: now}, time.Hour)
		require.NoError(t, err)
		assert.True(t, applied.Applied(),
			"a first write for a second filter key must report applied")

		result, err := c.GetSummary(ctx, key2)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "all-sum", result["all"].Summary)
		assert.Equal(t, "custom-sum", result["custom"].Summary)
	})
}

func TestClient_GetSummary(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	t.Run("not found returns nil", func(t *testing.T) {
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "nosum"}
		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, result)
	})
}

// TestClient_CreateSummary_EqualTimestampApplies pins that an equal cutoff is
// last-write-wins, so it reports an applied write rather than a stale skip.
func TestClient_CreateSummary_EqualTimestampApplies(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-equal"}
	at := time.Now().UTC()

	applied, err := c.CreateSummary(
		ctx, key, "all", &session.Summary{Summary: "first", UpdatedAt: at}, 0)
	require.NoError(t, err)
	require.True(t, applied.Applied())

	applied, err = c.CreateSummary(
		ctx, key, "all", &session.Summary{Summary: "second", UpdatedAt: at}, 0)
	require.NoError(t, err)
	assert.True(t, applied.Applied(), "an equal cutoff must remain last-write-wins")

	result, err := c.GetSummary(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "second", result["all"].Summary)
}

func TestClient_CreateSummary_PreservesExistingTTLOnUpdate(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	cfg := defaultConfig()
	cfg.SessionTTL = 10 * time.Second
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-ttl"}

	first := time.Now().UTC()
	_, err := c.CreateSummary(ctx, key, "all", &session.Summary{
		Summary:   "first",
		UpdatedAt: first,
	}, cfg.SessionTTL)
	require.NoError(t, err)

	sumKey := c.keys.SummaryKey(key)
	assert.Equal(t, 10*time.Second, mr.TTL(sumKey))

	mr.FastForward(4 * time.Second)

	_, err = c.CreateSummary(ctx, key, "all", &session.Summary{
		Summary:   "second",
		UpdatedAt: first.Add(time.Second),
	}, cfg.SessionTTL)
	require.NoError(t, err)

	assert.Equal(t, 6*time.Second, mr.TTL(sumKey))

	result, err := c.GetSummary(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "second", result["all"].Summary)
}

// TestClient_CreateSummary_StaleWriteKeepsTTL pins that a skipped stale write
// leaves the stored value and its TTL untouched.
func TestClient_CreateSummary_StaleWriteKeepsTTL(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	cfg := defaultConfig()
	cfg.SessionTTL = 10 * time.Second
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-stale-ttl"}

	at := time.Now().UTC()
	_, err := c.CreateSummary(
		ctx, key, "all", &session.Summary{Summary: "kept", UpdatedAt: at}, cfg.SessionTTL)
	require.NoError(t, err)

	sumKey := c.keys.SummaryKey(key)
	require.Equal(t, 10*time.Second, mr.TTL(sumKey))

	mr.FastForward(4 * time.Second)
	require.Equal(t, 6*time.Second, mr.TTL(sumKey))

	applied, err := c.CreateSummary(ctx, key, "all", &session.Summary{
		Summary:   "stale",
		UpdatedAt: at.Add(-time.Hour),
	}, cfg.SessionTTL)
	require.NoError(t, err)
	assert.False(t, applied.Applied())
	assert.Equal(t, 6*time.Second, mr.TTL(sumKey),
		"a skipped stale write must not refresh the TTL")

	result, err := c.GetSummary(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "kept", result["all"].Summary)
}

// TestClient_CreateSummary_LuaReplyContract restores the original Redis
// contract: a successful script execution is never turned into a caller-visible
// error just because the reply is unrecognized. Unknown replies stay unknown
// rather than stored or stale.
func TestClient_CreateSummary_LuaReplyContract(t *testing.T) {
	orig := luaSummarySetIfNewer
	t.Cleanup(func() { luaSummarySetIfNewer = orig })

	sum := &session.Summary{Summary: "reply-contract", UpdatedAt: time.Now().UTC()}

	t.Run("int64 1 is stored", func(t *testing.T) {
		luaSummarySetIfNewer = orig
		_, rdb := setupMiniredis(t)
		c := NewClient(rdb, defaultConfig())
		write, err := c.CreateSummary(
			context.Background(),
			session.Key{AppName: "app", UserID: "u1", SessionID: "lua-1"},
			"all", sum, 0,
		)
		require.NoError(t, err)
		assert.Equal(t, util.SummaryWriteApplied, write)
	})

	t.Run("int64 0 is stale", func(t *testing.T) {
		luaSummarySetIfNewer = orig
		_, rdb := setupMiniredis(t)
		c := NewClient(rdb, defaultConfig())
		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "u1", SessionID: "lua-0"}
		at := time.Now().UTC()
		_, err := c.CreateSummary(ctx, key, "all", &session.Summary{
			Summary: "kept", UpdatedAt: at,
		}, 0)
		require.NoError(t, err)
		write, err := c.CreateSummary(ctx, key, "all", &session.Summary{
			Summary: "older", UpdatedAt: at.Add(-time.Hour),
		}, 0)
		require.NoError(t, err)
		assert.Equal(t, util.SummaryWriteStale, write)
	})

	t.Run("unknown type does not add a business error", func(t *testing.T) {
		luaSummarySetIfNewer = redis.NewScript(`return "1"`)
		_, rdb := setupMiniredis(t)
		c := NewClient(rdb, defaultConfig())
		write, err := c.CreateSummary(
			context.Background(),
			session.Key{AppName: "app", UserID: "u1", SessionID: "lua-type"},
			"all", sum, 0,
		)
		require.NoError(t, err, "script err=nil must stay a nil CreateSummary error")
		assert.Equal(t, util.SummaryWriteUnknown, write)
		assert.False(t, write.Applied())
	})

	t.Run("unknown value does not add a business error", func(t *testing.T) {
		luaSummarySetIfNewer = redis.NewScript(`return 2`)
		_, rdb := setupMiniredis(t)
		c := NewClient(rdb, defaultConfig())
		write, err := c.CreateSummary(
			context.Background(),
			session.Key{AppName: "app", UserID: "u1", SessionID: "lua-value"},
			"all", sum, 0,
		)
		require.NoError(t, err, "script err=nil must stay a nil CreateSummary error")
		assert.Equal(t, util.SummaryWriteUnknown, write)
		assert.False(t, write.Applied())
	})

	t.Run("script error remains a store-summary error", func(t *testing.T) {
		luaSummarySetIfNewer = redis.NewScript(`return redis.error_reply("boom")`)
		_, rdb := setupMiniredis(t)
		c := NewClient(rdb, defaultConfig())
		write, err := c.CreateSummary(
			context.Background(),
			session.Key{AppName: "app", UserID: "u1", SessionID: "lua-err"},
			"all", sum, 0,
		)
		require.Error(t, err)
		assert.ErrorContains(t, err, "store summary failed")
		assert.False(t, write.Applied())
	})
}
