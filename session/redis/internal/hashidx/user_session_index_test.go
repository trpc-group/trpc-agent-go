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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestListSessionIDsFromUserIndex_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	userKey := session.UserKey{AppName: "app", UserID: "u1"}

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "idx-err1"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	_, err = c.listSessionIDsFromUserIndex(ctx, userKey)
	require.Error(t, err)
}

func TestListSessionIDsFromUserIndex_EmptyIndex(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	userKey := session.UserKey{AppName: "app", UserID: "no-sessions"}

	ids, err := c.listSessionIDsFromUserIndex(ctx, userKey)
	require.NoError(t, err)
	assert.Nil(t, ids)
}

func TestRemoveSessionFromUserIndex_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "u1", SessionID: "rm-err1"}
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	err = c.removeSessionFromUserIndex(ctx, []string{"some-key"}, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete session")
}
