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

// sessionIndexEntry is the value stored in the per-user session index Hash.
// Structured as JSON to allow future metadata extensions (e.g. lastActiveAt).
type sessionIndexEntry struct {
	CreatedAt time.Time `json:"createdAt"`
}

// addSessionToUserIndex atomically creates session meta (SET NX) and registers
// the session in the per-user index Hash via Lua script.
func (c *Client) addSessionToUserIndex(ctx context.Context, key session.Key, metaJSON []byte, now time.Time) error {
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	indexKey := c.keys.SessionIndexKey(userKey)
	metaKey := c.keys.SessionMetaKey(key)

	indexEntry, err := json.Marshal(sessionIndexEntry{CreatedAt: now})
	if err != nil {
		return fmt.Errorf("marshal index entry: %w", err)
	}

	ttlSeconds := int64(0)
	if c.cfg.SessionTTL > 0 {
		ttlSeconds = int64(c.cfg.SessionTTL.Seconds())
	}

	result, err := luaCreateSession.Run(ctx, c.client,
		[]string{metaKey, indexKey},
		string(metaJSON), key.SessionID, ttlSeconds, string(indexEntry),
	).Int()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if result == 0 {
		return fmt.Errorf("session already exists")
	}
	return nil
}

// removeSessionFromUserIndex removes a session entry from the per-user index Hash
// and deletes all associated data keys via Lua script.
func (c *Client) removeSessionFromUserIndex(ctx context.Context, dataKeys []string, key session.Key) error {
	userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
	indexKey := c.keys.SessionIndexKey(userKey)
	keys := make([]string, 0, len(dataKeys)+1)
	keys = append(keys, dataKeys...)
	keys = append(keys, indexKey)
	if _, err := luaDeleteSession.Run(ctx, c.client, keys, key.SessionID).Result(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

const userSessionHScanBatchSize = 100

// listSessionIDsFromUserIndex returns all session IDs stored in the per-user
// index Hash via HSCAN iteration.
func (c *Client) listSessionIDsFromUserIndex(ctx context.Context, userKey session.UserKey) ([]string, error) {
	indexKey := c.keys.SessionIndexKey(userKey)

	var fields []string
	var cursor uint64
	for {
		result, nextCursor, err := c.client.HScan(ctx, indexKey, cursor, "*", userSessionHScanBatchSize).Result()
		if err != nil {
			if err == redis.Nil {
				return nil, nil
			}
			return nil, err
		}
		for i := 0; i < len(result); i += 2 {
			fields = append(fields, result[i])
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return fields, nil
}

// cleanupStaleUserSessionIndexEntries removes orphaned session IDs from the index Hash.
// Called by ListSessions when meta keys have expired but index entries remain.
func (c *Client) cleanupStaleUserSessionIndexEntries(ctx context.Context, userKey session.UserKey, staleIDs []string) {
	if len(staleIDs) == 0 {
		return
	}
	indexKey := c.keys.SessionIndexKey(userKey)
	c.client.HDel(ctx, indexKey, staleIDs...)
}
