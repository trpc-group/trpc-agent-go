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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// newTrimBoundaryEvent builds an event that survives AppendEvent's validity
// check: it needs a non-nil Response carrying usable content.
func newTrimBoundaryEvent(id, reqID string, ts time.Time) *event.Event {
	return &event.Event{
		ID:        id,
		RequestID: reqID,
		Timestamp: ts,
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: id}},
			},
		},
	}
}

// appendTrimRound appends n events sharing reqID, one second apart each.
func appendTrimRound(t *testing.T, ctx context.Context, c *Client, key session.Key,
	reqID string, n int, start time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, c.AppendEvent(ctx, key, newTrimBoundaryEvent(
			fmt.Sprintf("%s-%d", reqID, i), reqID, start.Add(time.Duration(i)*time.Second))))
	}
}

// remainingTrimIDs returns the event IDs still stored in the session.
func remainingTrimIDs(t *testing.T, ctx context.Context, c *Client, key session.Key) []string {
	t.Helper()
	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	ids := make([]string, 0, len(sess.Events))
	for _, evt := range sess.Events {
		ids = append(ids, evt.ID)
	}
	return ids
}

// Scenario 1 of issue #2613: a single conversation larger than the Lua scan
// batch (100). Events past the first batch boundary still belong to the newest
// round and must be trimmed.
func TestTrimConversationsBoundary_CrossBatchSingleRound(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_b_single"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	appendTrimRound(t, ctx, c, key, "reqA", 101, time.Now())

	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)

	assert.Len(t, deleted, 101, "events past the batch boundary must still be trimmed")
	assert.Empty(t, remainingTrimIDs(t, ctx, c, key), "no event of the newest round may survive")
}

// Scenario 2 of issue #2613: two rounds whose combined size exceeds one batch.
// The older round keeps events beyond the batch boundary and they must be
// trimmed too.
func TestTrimConversationsBoundary_CrossBatchTwoRounds(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_b_two"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	now := time.Now()
	appendTrimRound(t, ctx, c, key, "reqA", 60, now)
	appendTrimRound(t, ctx, c, key, "reqB", 60, now.Add(time.Hour))

	deleted, err := c.TrimConversations(ctx, key, 2)
	require.NoError(t, err)

	assert.Len(t, deleted, 120, "both rounds must be trimmed completely")
	assert.Empty(t, remainingTrimIDs(t, ctx, c, key), "no event may survive")
}

// Scenario 3 of issue #2613: requests arrive as A1 -> B1 -> A2. The newest
// conversation is A, so A1 and A2 must both be trimmed even though B1 sits
// between them.
func TestTrimConversationsBoundary_InterleavedRounds(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_b_interleaved"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	now := time.Now()
	require.NoError(t, c.AppendEvent(ctx, key, newTrimBoundaryEvent("A1", "reqA", now)))
	require.NoError(t, c.AppendEvent(ctx, key, newTrimBoundaryEvent("B1", "reqB", now.Add(time.Second))))
	require.NoError(t, c.AppendEvent(ctx, key, newTrimBoundaryEvent("A2", "reqA", now.Add(2*time.Second))))

	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)

	deletedIDs := make([]string, 0, len(deleted))
	for _, evt := range deleted {
		deletedIDs = append(deletedIDs, evt.ID)
	}

	assert.ElementsMatch(t, []string{"A1", "A2"}, deletedIDs,
		"a round must be trimmed across interleaved foreign rounds")
	assert.Equal(t, []string{"B1"}, remainingTrimIDs(t, ctx, c, key))
}

// Scenario 4 of issue #2613: events without a RequestID never join a
// conversation, so they are neither counted nor trimmed.
func TestTrimConversationsBoundary_EmptyRequestIDPreserved(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "s_b_empty"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	now := time.Now()
	require.NoError(t, c.AppendEvent(ctx, key, newTrimBoundaryEvent("nk1", "", now)))
	appendTrimRound(t, ctx, c, key, "reqA", 2, now.Add(time.Second))
	require.NoError(t, c.AppendEvent(ctx, key,
		newTrimBoundaryEvent("nk2", "", now.Add(time.Hour))))

	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)

	assert.Len(t, deleted, 2, "only the newest conversation is trimmed")
	assert.ElementsMatch(t, []string{"nk1", "nk2"},
		remainingTrimIDs(t, ctx, c, key), "events without RequestID must survive")
}
