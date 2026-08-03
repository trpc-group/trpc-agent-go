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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// StateInitializationLeaseKey returns a lease key in the session's Redis
// Cluster hash slot. stateKeyDigest must not contain the raw state key.
func (c *Client) StateInitializationLeaseKey(
	key session.Key,
	stateKeyDigest string,
) string {
	return fmt.Sprintf(
		"%s:stateinit:%s:%s:%s:%s",
		c.keys.fullPrefix(),
		key.AppName,
		c.keys.hashTag(key.UserID),
		key.SessionID,
		stateKeyDigest,
	)
}

// LoadSessionStateValue loads one raw session-scoped state value.
func (c *Client) LoadSessionStateValue(
	ctx context.Context,
	key session.Key,
	stateKey string,
) ([]byte, bool, string, bool, error) {
	metaText, err := c.runScript(
		ctx,
		luaLoadStateInitializationValue,
		[]string{c.keys.SessionMetaKey(key)},
		uuid.NewString(),
	).Text()
	if err == redis.Nil {
		return nil, false, "", false, nil
	}
	if err != nil {
		return nil, false, "", false, fmt.Errorf("load session state value: %w", err)
	}
	var meta sessionMeta
	if err := json.Unmarshal([]byte(metaText), &meta); err != nil {
		return nil, false, "", true, fmt.Errorf("load session state value: unmarshal session meta: %w", err)
	}
	if meta.Generation == "" {
		return nil, false, "", true, fmt.Errorf("load session state value: session generation is missing")
	}
	value, present := meta.State[stateKey]
	return cloneStateInitializationValue(value), present, meta.Generation, true, nil
}

// CommitStateInitialization atomically persists a value and releases its
// owner-token-checked lease. It returns 1 on success, 0 when ownership was
// lost, -1 when the session disappeared, and -2 when the session generation
// changed.
func (c *Client) CommitStateInitialization(
	ctx context.Context,
	key session.Key,
	stateKey string,
	value []byte,
	generation string,
	leaseKey string,
	ownerToken string,
) (int, error) {
	ttlMillis := c.cfg.SessionTTL.Milliseconds()
	if c.cfg.SessionTTL > 0 && ttlMillis == 0 {
		ttlMillis = 1
	}
	valueIsNil := 0
	encodedValue := ""
	if value == nil {
		valueIsNil = 1
	} else {
		encodedValue = base64.StdEncoding.EncodeToString(value)
	}
	result, err := c.runScript(
		ctx,
		luaCommitStateInitialization,
		[]string{leaseKey, c.keys.SessionMetaKey(key)},
		ownerToken,
		stateKey,
		encodedValue,
		valueIsNil,
		generation,
		time.Now().UTC().Format(time.RFC3339Nano),
		ttlMillis,
	).Int()
	if err != nil {
		return 0, fmt.Errorf("commit state initialization: %w", err)
	}
	return result, nil
}

func cloneStateInitializationValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
