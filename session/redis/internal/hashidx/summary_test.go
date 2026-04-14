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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestClient_CreateSummary(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum1"}

	t.Run("creates new summary", func(t *testing.T) {
		now := time.Now()
		sum := &session.Summary{Summary: "test summary", UpdatedAt: now}
		require.NoError(t, c.CreateSummary(ctx, key, "all", sum, time.Hour))

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "test summary", result["all"].Summary)
	})

	t.Run("updates with newer timestamp", func(t *testing.T) {
		newer := time.Now().Add(time.Hour)
		sum := &session.Summary{Summary: "updated", UpdatedAt: newer}
		require.NoError(t, c.CreateSummary(ctx, key, "all", sum, time.Hour))

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "updated", result["all"].Summary)
	})

	t.Run("skips older timestamp", func(t *testing.T) {
		older := time.Now().Add(-24 * time.Hour)
		sum := &session.Summary{Summary: "old", UpdatedAt: older}
		require.NoError(t, c.CreateSummary(ctx, key, "all", sum, time.Hour))

		result, err := c.GetSummary(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "updated", result["all"].Summary)
	})

	t.Run("multiple filter keys", func(t *testing.T) {
		key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "sum2"}
		now := time.Now()

		require.NoError(t, c.CreateSummary(ctx, key2, "all", &session.Summary{Summary: "all-sum", UpdatedAt: now}, time.Hour))
		require.NoError(t, c.CreateSummary(ctx, key2, "custom", &session.Summary{Summary: "custom-sum", UpdatedAt: now}, time.Hour))

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
